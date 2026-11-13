// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// The engine's own mapping table behaviour needs a real Postgres: the
// unique index on (edge_id, source_event_id), the foreign key from
// event_mapping to edges, and the encrypted details column are the
// database's own, and a fake repository proves nothing about them. The
// build tag keeps this out of a plain "go test ./..."; "task
// test:integration" is what passes it.
package sync_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// integrationEncryptionKey is 32 bytes, matching the AES-256 key length
// postgres.NewStore requires. Its content is arbitrary and never used
// outside this test binary.
var integrationEncryptionKey = []byte("01234567890123456789012345678901")

// requireIntegrationDatabase returns DATABASE_URL, or stops the test:
// a skip where no CI is set, which is the local case with no Postgres
// reachable, and a failure where it is, so a job that lost its own env
// block reports a failure instead of reporting green having proven
// nothing.
func requireIntegrationDatabase(t *testing.T) string {
	t.Helper()

	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	if os.Getenv("CI") != "" {
		t.Fatal("DATABASE_URL not set while CI is set; the integration job lost its own env: block")
	}

	t.Skip("DATABASE_URL not set; skipping the Postgres-backed test")

	return ""
}

// TestEngineAgainstPostgres runs one edge end to end over the real
// mapping table: a reconcile that copies two events and records them,
// a second pass that writes nothing, and a deletion that gives back
// everything the edge wrote.
func TestEngineAgainstPostgres(t *testing.T) {
	databaseURL := requireIntegrationDatabase(t)

	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	store, err := postgres.NewStore(pool, integrationEncryptionKey)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	user, err := store.Users().Create(ctx, storage.User{
		Email:  "engine-" + uuid.NewString() + "@example.com",
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// An edge spans two provider accounts, and each connector hands out
	// calendar ids in its own space: both of these call their first
	// calendar "cal-1", because a provider's id is opaque and means
	// nothing outside the account it came from. calendars is unique on
	// (connection_id, external_id), so each calendar row belongs under
	// the connection it was actually read from, and putting both under
	// one connection makes the two rows collide on that index.
	source := newFakeConnector(defaultSourceCaps)
	target := newFakeConnector(defaultTargetCaps)
	sourceCal := source.addCalendar("Privat", provider.Calendar{TimeZone: "Europe/Berlin"})
	targetCal := target.addCalendar("Arbeit", provider.Calendar{TimeZone: "Europe/Berlin"})

	var edge storage.Edge

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		sourceConnection, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      user.ID,
			Provider:    storage.ConnectionProviderCalDAV,
			DisplayName: "iCloud",
			Credentials: []byte("app-specific-password"),
		})
		if err != nil {
			return fmt.Errorf("create the source connection: %w", err)
		}

		targetConnection, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      user.ID,
			Provider:    storage.ConnectionProviderMicrosoft,
			DisplayName: "Microsoft 365",
			Credentials: []byte("refresh-token"),
		})
		if err != nil {
			return fmt.Errorf("create the target connection: %w", err)
		}

		sourceRow, err := store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: sourceConnection.ID,
			ExternalID:   sourceCal,
			Name:         "Privat",
		})
		if err != nil {
			return fmt.Errorf("create the source calendar: %w", err)
		}

		targetRow, err := store.Calendars().Create(ctx, storage.Calendar{
			UserID:       user.ID,
			ConnectionID: targetConnection.ID,
			ExternalID:   targetCal,
			Name:         "Arbeit",
		})
		if err != nil {
			return fmt.Errorf("create the target calendar: %w", err)
		}

		edge, err = store.Edges().Create(ctx, storage.Edge{
			UserID:           user.ID,
			SourceCalendarID: sourceRow.ID,
			TargetCalendarID: targetRow.ID,
			DetailLevel:      storage.DetailLevelTitleTime,
		})
		if err != nil {
			return fmt.Errorf("create the edge: %w", err)
		}

		return nil
	}); err != nil {
		t.Fatalf("set up the edge: %v", err)
	}

	mask, err := sync.MaskFor(edge.DetailLevel)
	if err != nil {
		t.Fatalf("mask: %v", err)
	}

	credential := fakeCredential{connection: testConnection}
	binding := sync.Binding{
		Edge:   edge,
		Mask:   mask,
		Source: sync.Endpoint{Connector: source, Credential: credential, CalendarID: sourceCal, TimeZone: "Europe/Berlin"},
		Target: sync.Endpoint{Connector: target, Credential: credential, CalendarID: targetCal, TimeZone: "Europe/Berlin"},
	}

	binder := newFakeBinder()
	binder.add(binding)

	engine, err := sync.New(sync.Options{
		Scope:     store,
		Edges:     store.Edges(),
		Calendars: store.Calendars(),
		Grants:    store.CalendarGrants(),
		Mappings:  store.EventMappings(),
		Binder:    binder,
		Now:       func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	source.put(sourceCal, meeting("Jour fixe", 4, 9))
	source.put(sourceCal, meeting("Retro", 5, 14))

	first, err := engine.Reconcile(ctx, binding)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := first.Count(sync.ActionCreate); got != 2 {
		t.Fatalf("creates: got %d, want 2 (all: %v)", got, actions(first))
	}

	mappings := listMappings(ctx, t, store, user.ID, edge.ID)
	if len(mappings) != 2 {
		t.Fatalf("mapping rows in Postgres: got %d, want 2", len(mappings))
	}
	if mappings[0].ContentHash == "" || mappings[0].TargetEventID == "" {
		t.Fatalf("mapping row is incomplete: %+v", mappings[0])
	}
	if mappings[0].StartsAt.IsZero() || !mappings[0].EndsAt.After(mappings[0].StartsAt) {
		t.Fatalf("mapping row times: %s to %s", mappings[0].StartsAt, mappings[0].EndsAt)
	}

	second, err := engine.Reconcile(ctx, binding)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if got := second.CountReason(sync.ReasonUnchanged); got != 2 {
		t.Fatalf("unchanged: got %d, want 2 (all: %v)", got, actions(second))
	}
	if creates, updates, _ := target.counts(); creates != 2 || updates != 0 {
		t.Fatalf("connector calls after two passes: got %d creates and %d updates, want 2 and 0", creates, updates)
	}

	if _, err := engine.DeleteEdge(ctx, binding); err != nil {
		t.Fatalf("delete edge: %v", err)
	}

	if left := target.events(targetCal); len(left) != 0 {
		t.Fatalf("the target still holds %d events", len(left))
	}
	if left := listMappings(ctx, t, store, user.ID, edge.ID); len(left) != 0 {
		t.Fatalf("mapping rows after the deletion: got %d, want none", len(left))
	}

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		_, err := store.Edges().FindByID(ctx, edge.ID)

		return err
	}); err == nil {
		t.Fatal("the edge row is still there")
	}
}

// listMappings reads one edge's mapping rows through the store.
func listMappings(ctx context.Context, t *testing.T, store *postgres.Store, userID, edgeID uuid.UUID) []storage.EventMapping {
	t.Helper()

	var mappings []storage.EventMapping

	if err := store.WithUser(ctx, userID, func(ctx context.Context) error {
		var err error
		mappings, err = store.EventMappings().ListByEdge(ctx, edgeID)

		return err
	}); err != nil {
		t.Fatalf("list mappings: %v", err)
	}

	return mappings
}
