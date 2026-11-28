// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package mirror_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/mirror"
	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/providertest"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

func TestNewBinder(t *testing.T) {
	t.Parallel()

	w := newWorld(t)

	tests := []struct {
		name     string
		store    mirror.BinderStore
		registry *provider.Registry
		wantErr  error
	}{
		{name: "both given", store: w.store, registry: w.registry},
		{name: "no store", registry: w.registry, wantErr: mirror.ErrIncomplete},
		{name: "no registry", store: w.store, wantErr: mirror.ErrIncomplete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := mirror.NewBinder(tt.store, tt.registry)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NewBinder() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBinder_Bind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("resolves an owned source and the managed target", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelTitleTime)

		binder := newBinder(t, w)

		binding, err := binder.Bind(ctx, edge)
		if err != nil {
			t.Fatalf("Bind() error = %v", err)
		}

		if binding.Source.CalendarID != w.own.ExternalID || binding.Source.Managed {
			t.Errorf("source = %+v, want the grantee's own calendar, unmanaged", binding.Source)
		}
		if binding.Target.CalendarID != w.target.ExternalID || !binding.Target.Managed {
			t.Errorf("target = %+v, want the managed calendar", binding.Target)
		}
		if binding.Source.Credential == nil || binding.Source.Connector == nil {
			t.Errorf("source carries no connector or credential: %+v", binding.Source)
		}
	})

	t.Run("reads a shared source with the owner's credential", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.shared.ID, storage.DetailLevelBusyBlock)

		binding, err := newBinder(t, w).Bind(ctx, edge)
		if err != nil {
			t.Fatalf("Bind() error = %v", err)
		}

		if binding.Source.CalendarID != w.shared.ExternalID {
			t.Errorf("source calendar = %q, want %q", binding.Source.CalendarID, w.shared.ExternalID)
		}

		want := providertest.CredentialFor(w.ownerConnection.ID.String())
		if binding.Source.Credential != want {
			t.Errorf("source credential = %v, want the owner's %v", binding.Source.Credential, want)
		}
	})

	t.Run("returns an empty source once the grant is revoked", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.shared.ID, storage.DetailLevelBusyBlock)
		w.revoke(ctx, t)

		binding, err := newBinder(t, w).Bind(ctx, edge)
		if err != nil {
			t.Fatalf("Bind() error = %v", err)
		}

		if binding.Source.Connector != nil || binding.Source.CalendarID != "" {
			t.Errorf("source = %+v, want the zero endpoint", binding.Source)
		}
		if binding.Target.CalendarID != w.target.ExternalID {
			t.Errorf("target = %+v, want it resolved so a teardown can run", binding.Target)
		}
	})

	t.Run("refuses a detail level it does not know", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		edge.DetailLevel = "everything"

		_, err := newBinder(t, w).Bind(ctx, edge)
		if err == nil {
			t.Fatal("Bind() accepted an unknown detail level")
		}
	})

	t.Run("refuses a provider no connector is registered for", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)

		binder, err := mirror.NewBinder(w.store, provider.NewRegistry())
		if err != nil {
			t.Fatalf("NewBinder() error = %v", err)
		}

		_, err = binder.Bind(ctx, edge)
		if !errors.Is(err, provider.ErrUnknownProvider) {
			t.Errorf("Bind() error = %v, want ErrUnknownProvider", err)
		}
	})

	t.Run("refuses a stored credential its decoder rejects", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		garbled := w.connection(ctx, t, w.grantee, "garbled")
		broken := w.calendar(ctx, t, w.grantee, garbled, false)
		edge := w.edge(ctx, t, broken.ID, storage.DetailLevelFull)

		_, err := newBinder(t, w).Bind(ctx, edge)
		if !errors.Is(err, provider.ErrCredentialMismatch) {
			t.Errorf("Bind() error = %v, want ErrCredentialMismatch", err)
		}
	})

	t.Run("reads the target in the edge owner's scope only", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		edge.TargetCalendarID = w.shared.ID

		_, err := newBinder(t, w).Bind(ctx, edge)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Bind() error = %v, want ErrNotFound for a target the user does not own", err)
		}
	})
}

func TestBinder_Connect(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	w := newWorld(t)
	binder := newBinder(t, w)

	// The fake store's scope is one field, so these two share it in turn.
	t.Run("resolves the user's own connection", func(t *testing.T) {
		connector, credential, err := binder.Connect(ctx, w.grantee, w.granteeConnection.ID)
		if err != nil {
			t.Fatalf("Connect() error = %v", err)
		}
		if connector.ID() != providertest.ProviderID || credential == nil {
			t.Errorf("Connect() = %v, %v", connector.ID(), credential)
		}
	})

	t.Run("refuses another user's connection", func(t *testing.T) {
		_, _, err := binder.Connect(ctx, w.grantee, w.ownerConnection.ID)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Connect() error = %v, want ErrNotFound", err)
		}
	})
}

// The binder satisfies the engine's own interface.
var _ sync.Binder = (*mirror.Binder)(nil)

func newBinder(t *testing.T, w *world) *mirror.Binder {
	t.Helper()

	binder, err := mirror.NewBinder(w.store, w.registry)
	if err != nil {
		t.Fatalf("NewBinder() error = %v", err)
	}

	return binder
}
