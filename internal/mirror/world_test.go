// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package mirror_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/providertest"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// validCredential is the stored form the test connector accepts. Any other
// stored form fails to decode.
const validCredential = "valid"

// decoding wraps the in-memory connector so a stored credential can be
// written before the connection's id exists. The connector's own decoder
// demands the id as the stored form.
type decoding struct {
	*providertest.Connector
}

func (d decoding) DecodeCredential(connection string, raw []byte) (provider.Credential, error) {
	if string(raw) != validCredential {
		return nil, fmt.Errorf("connection %s: %w", connection, provider.ErrCredentialMismatch)
	}

	return providertest.CredentialFor(connection), nil
}

// world is a small account graph over the fake store: an owner with one
// shared calendar, and a grantee with a calendar of their own and a target
// calbridge created for them.
type world struct {
	store    *storagetest.Store
	provider *providertest.Connector
	registry *provider.Registry

	owner, grantee uuid.UUID

	ownerConnection   storage.Connection
	granteeConnection storage.Connection

	shared storage.Calendar // the owner's calendar, granted to the grantee
	own    storage.Calendar // the grantee's own calendar
	target storage.Calendar // a calendar calbridge created for the grantee
	grant  storage.CalendarGrant
}

func newWorld(t *testing.T) *world {
	t.Helper()

	ctx := context.Background()
	connector := providertest.New(provider.Capabilities{CalendarCreation: true, FloatingTimes: true})

	registry := provider.NewRegistry()
	if err := registry.Register(decoding{connector}); err != nil {
		t.Fatalf("register connector: %v", err)
	}

	w := &world{
		store:    storagetest.NewStore(),
		provider: connector,
		registry: registry,
		owner:    uuid.New(),
		grantee:  uuid.New(),
	}

	w.ownerConnection = w.connection(ctx, t, w.owner, validCredential)
	w.granteeConnection = w.connection(ctx, t, w.grantee, validCredential)
	w.shared = w.calendar(ctx, t, w.owner, w.ownerConnection, false)
	w.own = w.calendar(ctx, t, w.grantee, w.granteeConnection, false)
	w.target = w.calendar(ctx, t, w.grantee, w.granteeConnection, true)

	err := w.store.WithUser(ctx, w.owner, func(ctx context.Context) error {
		grant, err := w.store.CalendarGrants().Create(ctx, storage.CalendarGrant{
			OwnerUserID:    w.owner,
			GranteeUserID:  w.grantee,
			CalendarID:     w.shared.ID,
			MaxDetailLevel: storage.DetailLevelFull,
		})
		w.grant = grant

		return err
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}

	return w
}

func (w *world) connection(ctx context.Context, t *testing.T, userID uuid.UUID, credential string) storage.Connection {
	t.Helper()

	var created storage.Connection

	err := w.store.WithUser(ctx, userID, func(ctx context.Context) error {
		connection, err := w.store.Connections().Create(ctx, storage.Connection{
			UserID:      userID,
			Provider:    storage.ConnectionProvider(providertest.ProviderID),
			DisplayName: "Work",
			Credentials: []byte(credential),
		})
		created = connection

		return err
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	return created
}

// calendar adds a calendar at the provider and records it for the user.
func (w *world) calendar(
	ctx context.Context,
	t *testing.T,
	userID uuid.UUID,
	connection storage.Connection,
	managed bool,
) storage.Calendar {
	t.Helper()

	remote := w.provider.AddCalendar(connection.ID.String(), provider.Calendar{Name: "Team"})

	var created storage.Calendar

	err := w.store.WithUser(ctx, userID, func(ctx context.Context) error {
		calendar, err := w.store.Calendars().Create(ctx, storage.Calendar{
			UserID:       userID,
			ConnectionID: connection.ID,
			ExternalID:   remote.ID,
			Name:         remote.Name,
			Managed:      managed,
		})
		created = calendar

		return err
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}

	return created
}

// edge creates an edge for the grantee from source to the world's target.
func (w *world) edge(ctx context.Context, t *testing.T, source uuid.UUID, level storage.DetailLevel) storage.Edge {
	t.Helper()

	var created storage.Edge

	err := w.store.WithUser(ctx, w.grantee, func(ctx context.Context) error {
		edge, err := w.store.Edges().Create(ctx, storage.Edge{
			UserID:           w.grantee,
			SourceCalendarID: source,
			TargetCalendarID: w.target.ID,
			DetailLevel:      level,
		})
		created = edge

		return err
	})
	if err != nil {
		t.Fatalf("create edge: %v", err)
	}

	return created
}

// revoke removes the grant, as the grant route does.
func (w *world) revoke(ctx context.Context, t *testing.T) {
	t.Helper()

	err := w.store.WithUser(ctx, w.owner, func(ctx context.Context) error {
		return w.store.CalendarGrants().Delete(ctx, w.grant.ID)
	})
	if err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
}
