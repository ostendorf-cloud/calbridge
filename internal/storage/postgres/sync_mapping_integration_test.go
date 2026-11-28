// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

//go:build integration

// The mapping and edge queries the sync engine added need a real
// Postgres, for the same reason the rest of this package's integration
// tests do: row level security and the encrypted details column are the
// database's own behaviour, not something a fake can stand in for. See
// integration_test.go for the build tag and the DATABASE_URL handling.
package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres"
)

// syncFixture is one user with a source calendar, a target calendar and
// an edge between them, which is what every case below starts from.
type syncFixture struct {
	store  *postgres.Store
	user   storage.User
	source storage.Calendar
	target storage.Calendar
	edge   storage.Edge
}

// newSyncFixture creates the rows one edge needs, through the restricted
// role, so every assertion below runs under row level security.
func newSyncFixture(ctx context.Context, t *testing.T, store *postgres.Store) syncFixture {
	t.Helper()

	user, err := store.Users().Create(ctx, storage.User{
		Email:  "sync-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	fixture := syncFixture{store: store, user: user}

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		connection, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      user.ID,
			Provider:    storage.ConnectionProviderCalDAV,
			DisplayName: "iCloud",
			Credentials: []byte("app-specific-password"),
		})
		if err != nil {
			return fmt.Errorf("create the connection: %w", err)
		}

		// calendars is unique on (connection_id, external_id), so two
		// calendars under one connection need two distinct provider ids.
		fixture.source, err = store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: connection.ID,
			ExternalID:   "privat",
			Name:         "Privat",
		})
		if err != nil {
			return fmt.Errorf("create the source calendar: %w", err)
		}

		fixture.target, err = store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: connection.ID,
			ExternalID:   "arbeit",
			Name:         "Arbeit",
			Managed:      true,
		})
		if err != nil {
			return fmt.Errorf("create the target calendar: %w", err)
		}

		fixture.edge, err = store.Edges().Create(ctx, storage.Edge{
			UserID:           user.ID,
			SourceCalendarID: fixture.source.ID,
			TargetCalendarID: fixture.target.ID,
			DetailLevel:      storage.DetailLevelTitleTime,
		})
		if err != nil {
			return fmt.Errorf("create the edge: %w", err)
		}

		return nil
	}); err != nil {
		t.Fatalf("set up the edge: %v", err)
	}

	return fixture
}

// mapping records one copy of a source event, the way internal/sync
// records it.
func (f syncFixture) mapping(ctx context.Context, t *testing.T, sourceEventID, targetEventID, summary string) {
	t.Helper()

	if err := f.store.WithUser(ctx, f.user.ID, func(ctx context.Context) error {
		_, err := f.store.EventMappings().Create(ctx, storage.EventMapping{
			UserID:        f.user.ID,
			EdgeID:        f.edge.ID,
			SourceEventID: sourceEventID,
			TargetEventID: targetEventID,
			StartsAt:      time.Date(2026, time.March, 4, 8, 0, 0, 0, time.UTC),
			EndsAt:        time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC),
			ContentHash:   "hash-" + sourceEventID,
			Details:       []byte(`{"summary":"` + summary + `"}`),
		})

		return err
	}); err != nil {
		t.Fatalf("create mapping for %q: %v", sourceEventID, err)
	}
}

func TestEventMappingListByEdgeAndDeleteByEdge(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	restrictedURL := setUpTestAppRole(ctx, t, adminPool, adminURL)

	pool, err := postgres.NewPool(ctx, restrictedURL)
	if err != nil {
		t.Fatalf("open restricted pool as %s: %v", testAppRole, err)
	}
	t.Cleanup(pool.Close)

	store, err := postgres.NewStore(pool, testEncryptionKey)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	fixture := newSyncFixture(ctx, t, store)
	other := newSyncFixture(ctx, t, store)

	fixture.mapping(ctx, t, "evt-1", "copy-1", "Jour fixe")
	fixture.mapping(ctx, t, "evt-2", "copy-2", "Retro")
	other.mapping(ctx, t, "evt-1", "copy-9", "Fremder Termin")

	t.Run("ListByEdge returns every copy the edge made", func(t *testing.T) {
		var mappings []storage.EventMapping

		if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
			var err error
			mappings, err = store.EventMappings().ListByEdge(ctx, fixture.edge.ID)

			return err
		}); err != nil {
			t.Fatalf("list mappings: %v", err)
		}

		if len(mappings) != 2 {
			t.Fatalf("mappings: got %d, want 2", len(mappings))
		}
		if mappings[0].SourceEventID != "evt-1" || mappings[1].SourceEventID != "evt-2" {
			t.Fatalf("order: got %q and %q, want evt-1 then evt-2", mappings[0].SourceEventID, mappings[1].SourceEventID)
		}
		if string(mappings[0].Details) != `{"summary":"Jour fixe"}` {
			t.Fatalf("details came back as %q, want them decrypted", mappings[0].Details)
		}
	})

	t.Run("ListByEdge reaches no other user's edge", func(t *testing.T) {
		var mappings []storage.EventMapping

		if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
			var err error
			mappings, err = store.EventMappings().ListByEdge(ctx, other.edge.ID)

			return err
		}); err != nil {
			t.Fatalf("list mappings: %v", err)
		}

		if len(mappings) != 0 {
			t.Fatalf("mappings of another user's edge: got %d, want none", len(mappings))
		}
	})

	t.Run("ListByEdge refuses a call with no user scope", func(t *testing.T) {
		_, err := store.EventMappings().ListByEdge(ctx, fixture.edge.ID)
		if !errors.Is(err, storage.ErrNoUserScope) {
			t.Fatalf("got %v, want %v", err, storage.ErrNoUserScope)
		}
	})

	t.Run("the cached details never reach the database in clear", func(t *testing.T) {
		var stored []byte

		row := adminPool.QueryRow(ctx,
			`SELECT encrypted_details FROM event_mapping WHERE edge_id = $1 AND source_event_id = $2`,
			fixture.edge.ID, "evt-1")
		if err := row.Scan(&stored); err != nil {
			t.Fatalf("read encrypted_details: %v", err)
		}

		if bytes.Contains(stored, []byte("Jour fixe")) {
			t.Fatal("the copied title is readable in the database")
		}
	})

	t.Run("DeleteByEdge removes the edge's copies and no others", func(t *testing.T) {
		var removed int64

		if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
			var err error
			removed, err = store.EventMappings().DeleteByEdge(ctx, fixture.edge.ID)

			return err
		}); err != nil {
			t.Fatalf("delete mappings: %v", err)
		}

		if removed != 2 {
			t.Fatalf("removed: got %d, want 2", removed)
		}

		var left []storage.EventMapping
		if err := store.WithUser(ctx, other.user.ID, func(ctx context.Context) error {
			var err error
			left, err = store.EventMappings().ListByEdge(ctx, other.edge.ID)

			return err
		}); err != nil {
			t.Fatalf("list the other user's mappings: %v", err)
		}
		if len(left) != 1 {
			t.Fatalf("the other user's mappings: got %d, want 1", len(left))
		}
	})

	t.Run("the edge row can be deleted once its mappings are gone", func(t *testing.T) {
		if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
			return store.Edges().Delete(ctx, fixture.edge.ID)
		}); err != nil {
			t.Fatalf("delete edge: %v", err)
		}
	})

	t.Run("DeleteByEdge reports nothing removed for an edge that wrote nothing", func(t *testing.T) {
		var removed int64

		if err := store.WithUser(ctx, other.user.ID, func(ctx context.Context) error {
			var err error
			removed, err = store.EventMappings().DeleteByEdge(ctx, uuid.New())

			return err
		}); err != nil {
			t.Fatalf("delete mappings: %v", err)
		}

		if removed != 0 {
			t.Fatalf("removed: got %d, want 0", removed)
		}
	})
}

func TestEdgeListByCalendar(t *testing.T) {
	adminURL := requireDatabaseURL(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	restrictedURL := setUpTestAppRole(ctx, t, adminPool, adminURL)

	pool, err := postgres.NewPool(ctx, restrictedURL)
	if err != nil {
		t.Fatalf("open restricted pool as %s: %v", testAppRole, err)
	}
	t.Cleanup(pool.Close)

	store, err := postgres.NewStore(pool, testEncryptionKey)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	fixture := newSyncFixture(ctx, t, store)
	other := newSyncFixture(ctx, t, store)

	t.Run("an edge is found from either of its calendars", func(t *testing.T) {
		for _, calendar := range []storage.Calendar{fixture.source, fixture.target} {
			var edges []storage.Edge

			if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
				var err error
				edges, err = store.Edges().ListByCalendar(ctx, calendar.ID)

				return err
			}); err != nil {
				t.Fatalf("list edges over %q: %v", calendar.Name, err)
			}

			if len(edges) != 1 || edges[0].ID != fixture.edge.ID {
				t.Fatalf("edges over %q: got %+v, want the one edge", calendar.Name, edges)
			}
		}
	})

	t.Run("a calendar no edge touches has none", func(t *testing.T) {
		var edges []storage.Edge

		if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
			var err error
			edges, err = store.Edges().ListByCalendar(ctx, uuid.New())

			return err
		}); err != nil {
			t.Fatalf("list edges: %v", err)
		}

		if len(edges) != 0 {
			t.Fatalf("edges: got %d, want none", len(edges))
		}
	})

	t.Run("another user's edge over their own calendar stays invisible", func(t *testing.T) {
		var edges []storage.Edge

		if err := store.WithUser(ctx, fixture.user.ID, func(ctx context.Context) error {
			var err error
			edges, err = store.Edges().ListByCalendar(ctx, other.source.ID)

			return err
		}); err != nil {
			t.Fatalf("list edges: %v", err)
		}

		if len(edges) != 0 {
			t.Fatalf("edges over another user's calendar: got %d, want none", len(edges))
		}
	})

	t.Run("ListByCalendar refuses a call with no user scope", func(t *testing.T) {
		_, err := store.Edges().ListByCalendar(ctx, fixture.source.ID)
		if !errors.Is(err, storage.ErrNoUserScope) {
			t.Fatalf("got %v, want %v", err, storage.ErrNoUserScope)
		}
	})
}
