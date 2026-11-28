// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package mirror_test

import (
	"context"
	"errors"
	"fmt"
	stdsync "sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/mirror"
	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// fakeEngine records what the service asks of the engine. Where the real
// engine removes edge rows, this one does too, so the calendar collection
// that follows sees the state the real engine leaves.
type fakeEngine struct {
	store *storagetest.Store

	mu          stdsync.Mutex
	coverage    sync.Coverage
	reconcile   error
	deleteErr   error
	reconciled  []sync.Binding
	deleted     []sync.Binding
	enforced    []storage.CalendarGrant
	reconcileCh chan struct{}
	enforcedCh  chan struct{}

	// gate, when set, holds every Reconcile until it is closed, so a test
	// can keep a pass over one user running.
	gate chan struct{}
}

func newFakeEngine(store *storagetest.Store) *fakeEngine {
	return &fakeEngine{
		store:       store,
		coverage:    sync.Coverage{OwnsSource: true, OwnsTarget: true},
		reconcileCh: make(chan struct{}, 16),
		enforcedCh:  make(chan struct{}, 16),
	}
}

func (f *fakeEngine) Coverage(context.Context, storage.Edge) (sync.Coverage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.coverage, nil
}

func (f *fakeEngine) Reconcile(_ context.Context, b sync.Binding) (sync.Report, error) {
	f.mu.Lock()
	f.reconciled = append(f.reconciled, b)
	err := f.reconcile
	f.mu.Unlock()

	f.reconcileCh <- struct{}{}

	if f.gate != nil {
		<-f.gate
	}

	return sync.Report{}, err
}

func (f *fakeEngine) DeleteEdge(ctx context.Context, b sync.Binding) (sync.Report, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, b)
	err := f.deleteErr
	f.mu.Unlock()

	// The engine's own validation refuses a binding with no source.
	if b.Source.Connector == nil {
		return sync.Report{}, fmt.Errorf("source endpoint has no connector: %w", sync.ErrInvalidBinding)
	}
	if err != nil {
		return sync.Report{}, err
	}

	return sync.Report{}, f.removeEdge(ctx, b.Edge)
}

func (f *fakeEngine) EnforceGrants(ctx context.Context, grant storage.CalendarGrant) (sync.Report, error) {
	f.mu.Lock()
	f.enforced = append(f.enforced, grant)
	f.mu.Unlock()

	f.enforcedCh <- struct{}{}

	var affected []storage.Edge

	err := f.store.WithUser(ctx, grant.GranteeUserID, func(ctx context.Context) error {
		found, err := f.store.Edges().ListByCalendar(ctx, grant.CalendarID)
		affected = found

		return err
	})
	if err != nil {
		return sync.Report{}, err
	}

	for _, edge := range affected {
		if err := f.removeEdge(ctx, edge); err != nil {
			return sync.Report{}, err
		}
	}

	return sync.Report{}, nil
}

func (f *fakeEngine) removeEdge(ctx context.Context, edge storage.Edge) error {
	return f.store.WithUser(ctx, edge.UserID, func(ctx context.Context) error {
		return f.store.Edges().Delete(ctx, edge.ID)
	})
}

func (f *fakeEngine) reconciledCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.reconciled)
}

func (f *fakeEngine) enforcedGrants() []storage.CalendarGrant {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]storage.CalendarGrant(nil), f.enforced...)
}

func (f *fakeEngine) setReconcile(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reconcile = err
}

// noOwners hides every edge owner from the scheduled pass, so a test can
// tell a queued trigger apart from the pass Run makes at start.
type noOwners struct{ *storagetest.Store }

func (noOwners) ListEdgeOwners(context.Context) ([]uuid.UUID, error) { return nil, nil }

func newService(t *testing.T, w *world, store mirror.Store, engine mirror.Engine) *mirror.Service {
	t.Helper()

	service, err := mirror.New(mirror.Options{
		Store:    store,
		Engine:   engine,
		Binder:   newBinder(t, w),
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("mirror.New() error = %v", err)
	}

	return service
}

// runUntil starts Run and returns a stop function that cancels it and
// waits for it to return, so the test touches the fake store alone.
func runUntil(t *testing.T, service *mirror.Service) (stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- service.Run(ctx) }()

	return func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Run() error = %v, want nil on cancel", err)
		}
	}
}

func waitFor(t *testing.T, signal <-chan struct{}) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not act on its trigger")
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	engine := newFakeEngine(w.store)
	binder := newBinder(t, w)

	tests := []struct {
		name    string
		opts    mirror.Options
		wantErr error
	}{
		{
			name: "complete",
			opts: mirror.Options{Store: w.store, Engine: engine, Binder: binder, Interval: time.Minute},
		},
		{
			name:    "no binder",
			opts:    mirror.Options{Store: w.store, Engine: engine, Interval: time.Minute},
			wantErr: mirror.ErrIncomplete,
		},
		{
			name:    "no engine",
			opts:    mirror.Options{Store: w.store, Binder: binder, Interval: time.Minute},
			wantErr: mirror.ErrIncomplete,
		},
		{
			name:    "no store",
			opts:    mirror.Options{Engine: engine, Binder: binder, Interval: time.Minute},
			wantErr: mirror.ErrIncomplete,
		},
		{
			name:    "no interval",
			opts:    mirror.Options{Store: w.store, Engine: engine, Binder: binder},
			wantErr: mirror.ErrIncomplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := mirror.New(tt.opts)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestService_Pass(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("reconciles every edge of a user who owns one", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)

		newService(t, w, w.store, engine).Pass(ctx)

		if len(engine.reconciled) != 1 || engine.reconciled[0].Edge.ID != edge.ID {
			t.Fatalf("reconciled = %+v, want edge %s once", engine.reconciled, edge.ID)
		}
	})

	t.Run("tears down an edge no grant covers instead of reconciling it", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.shared.ID, storage.DetailLevelFull)
		w.revoke(ctx, t)

		engine := newFakeEngine(w.store)
		engine.coverage = sync.Coverage{OwnsSource: false, OwnsTarget: true}

		newService(t, w, w.store, engine).Pass(ctx)

		if engine.reconciledCount() != 0 {
			t.Errorf("reconciled %d times, want an uncovered edge left alone", engine.reconciledCount())
		}

		enforced := engine.enforcedGrants()
		if len(enforced) != 2 {
			t.Fatalf("enforced %d grants, want one per calendar of the edge", len(enforced))
		}

		for _, grant := range enforced {
			if grant.GranteeUserID != edge.UserID {
				t.Errorf("enforced grantee = %s, want the edge's owner %s", grant.GranteeUserID, edge.UserID)
			}
		}

		if _, ok := w.store.Edge(edge.ID); ok {
			t.Error("the uncovered edge is still stored")
		}
	})
}

func TestService_CredentialRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	refused := fmt.Errorf("reconcile: %w", provider.ErrCredentialRejected)

	tests := []struct {
		name string
		errs []error
		want bool
	}{
		{name: "a refused credential is recorded", errs: []error{refused}, want: true},
		{name: "a clean pass clears it", errs: []error{refused, nil}, want: false},
		{name: "another failure leaves it standing", errs: []error{refused, errors.New("timeout")}, want: true},
		{name: "another failure alone records nothing", errs: []error{errors.New("timeout")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(t)
			edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
			engine := newFakeEngine(w.store)
			service := newService(t, w, w.store, engine)

			for _, err := range tt.errs {
				engine.setReconcile(err)
				service.Pass(ctx)
			}

			if got := service.CredentialRefused(edge.ID); got != tt.want {
				t.Errorf("CredentialRefused() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestService_Run(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("reconciles a new edge without a scheduled pass", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)
		service := newService(t, w, noOwners{w.store}, engine)

		stop := runUntil(t, service)
		service.EdgeCreated(w.grantee, edge.ID)
		waitFor(t, engine.reconcileCh)
		stop()

		if engine.reconciled[0].Edge.ID != edge.ID {
			t.Errorf("reconciled edge = %s, want %s", engine.reconciled[0].Edge.ID, edge.ID)
		}
	})

	t.Run("tears down the edges a revoked grant covered", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.shared.ID, storage.DetailLevelFull)
		w.revoke(ctx, t)

		engine := newFakeEngine(w.store)
		service := newService(t, w, noOwners{w.store}, engine)

		stop := runUntil(t, service)
		service.GrantRevoked(w.grant)
		waitFor(t, engine.enforcedCh)
		stop()

		enforced := engine.enforcedGrants()
		if len(enforced) != 1 || enforced[0].ID != w.grant.ID {
			t.Fatalf("enforced = %+v, want the revoked grant", enforced)
		}
		if _, ok := w.store.Edge(edge.ID); ok {
			t.Error("the edge the grant covered is still stored")
		}
		if got := w.store.CountCalendars(); got != 2 {
			t.Errorf("calendars = %d, want the managed target collected and two left", got)
		}
	})

	t.Run("makes a full pass at start", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)

		stop := runUntil(t, newService(t, w, w.store, engine))
		waitFor(t, engine.reconcileCh)
		stop()
	})

	t.Run("returns nil when its context ends", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		service := newService(t, w, w.store, newFakeEngine(w.store))

		runCtx, cancel := context.WithCancel(ctx)
		cancel()

		if err := service.Run(runCtx); err != nil {
			t.Errorf("Run() error = %v, want nil", err)
		}
	})

	t.Run("never blocks a caller when the queue is full", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		service := newService(t, w, w.store, newFakeEngine(w.store))

		for range 1000 {
			service.EdgeCreated(w.grantee, uuid.New())
			service.GrantRevoked(w.grant)
		}
	})
}

func TestService_DeleteEdge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("takes the edge down and collects the calendar it created", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)

		if err := newService(t, w, w.store, engine).DeleteEdge(ctx, w.grantee, edge.ID); err != nil {
			t.Fatalf("DeleteEdge() error = %v", err)
		}

		if len(engine.deleted) != 1 || engine.deleted[0].Edge.ID != edge.ID {
			t.Fatalf("engine deleted %+v, want edge %s", engine.deleted, edge.ID)
		}
		if _, ok := w.store.Edge(edge.ID); ok {
			t.Error("the edge row is still stored")
		}
		if got := w.store.CountCalendars(); got != 2 {
			t.Errorf("calendars = %d, want 2: the managed target is collected, the others stay", got)
		}
	})

	t.Run("keeps the edge and reports a refused credential", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)
		engine.deleteErr = fmt.Errorf("remove events: %w", provider.ErrCredentialRejected)
		service := newService(t, w, w.store, engine)

		err := service.DeleteEdge(ctx, w.grantee, edge.ID)
		if !errors.Is(err, provider.ErrCredentialRejected) {
			t.Fatalf("DeleteEdge() error = %v, want ErrCredentialRejected", err)
		}
		if _, ok := w.store.Edge(edge.ID); !ok {
			t.Error("the edge row went although its copies were not removed")
		}
		if !service.CredentialRefused(edge.ID) {
			t.Error("the refusal was not recorded for the edge")
		}
		if got := w.store.CountCalendars(); got != 3 {
			t.Errorf("calendars = %d, want 3: nothing is collected after a failed delete", got)
		}
	})

	t.Run("falls back to the grant teardown when the source is gone", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.shared.ID, storage.DetailLevelFull)
		w.revoke(ctx, t)
		engine := newFakeEngine(w.store)

		if err := newService(t, w, w.store, engine).DeleteEdge(ctx, w.grantee, edge.ID); err != nil {
			t.Fatalf("DeleteEdge() error = %v", err)
		}

		if len(engine.enforcedGrants()) == 0 {
			t.Error("the engine's grant teardown never ran for an edge whose source is gone")
		}
		if _, ok := w.store.Edge(edge.ID); ok {
			t.Error("the edge row is still stored")
		}
	})

	t.Run("refuses an edge of another user", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)

		err := newService(t, w, w.store, engine).DeleteEdge(ctx, w.owner, edge.ID)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("DeleteEdge() error = %v, want ErrNotFound", err)
		}
		if len(engine.deleted) != 0 {
			t.Error("the engine was asked to delete an edge the caller does not own")
		}
	})

	t.Run("gives up waiting for the user's lock when its context ends", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
		engine := newFakeEngine(w.store)
		engine.gate = make(chan struct{})
		service := newService(t, w, w.store, engine)

		// A pass over the same user holds the lock until the gate opens.
		passDone := make(chan struct{})

		go func() {
			defer close(passDone)

			service.Pass(ctx)
		}()

		t.Cleanup(func() {
			close(engine.gate)
			<-passDone
		})

		waitFor(t, engine.reconcileCh)

		callCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		result := make(chan error, 1)

		go func() { result <- service.DeleteEdge(callCtx, w.grantee, edge.ID) }()

		select {
		case err := <-result:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("DeleteEdge() error = %v, want the context's deadline", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("DeleteEdge kept waiting for the lock after its context ended")
		}

		if _, ok := w.store.Edge(edge.ID); !ok {
			t.Error("the edge row went although the delete never ran")
		}
	})
}

func TestService_CreateCalendar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("creates the calendar at the provider and returns its own id", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		service := newService(t, w, w.store, newFakeEngine(w.store))

		id, err := service.CreateCalendar(ctx, w.grantee, w.granteeConnection.ID, "Mirror")
		if err != nil {
			t.Fatalf("CreateCalendar() error = %v", err)
		}

		found := false

		for _, calendar := range mustCalendars(ctx, t, w) {
			found = found || calendar.ID == id
		}

		if !found {
			t.Errorf("provider holds no calendar %q after CreateCalendar", id)
		}
	})

	t.Run("discards a calendar it created", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		service := newService(t, w, w.store, newFakeEngine(w.store))

		id, err := service.CreateCalendar(ctx, w.grantee, w.granteeConnection.ID, "Mirror")
		if err != nil {
			t.Fatalf("CreateCalendar() error = %v", err)
		}

		if err := service.DiscardCalendar(ctx, w.grantee, w.granteeConnection.ID, id); err != nil {
			t.Fatalf("DiscardCalendar() error = %v", err)
		}

		for _, calendar := range mustCalendars(ctx, t, w) {
			if calendar.ID == id {
				t.Errorf("provider still holds calendar %q after DiscardCalendar", id)
			}
		}
	})

	t.Run("refuses a provider that creates no calendar", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		w.provider.SetCapabilities(provider.Capabilities{})
		service := newService(t, w, w.store, newFakeEngine(w.store))

		_, err := service.CreateCalendar(ctx, w.grantee, w.granteeConnection.ID, "Mirror")
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("CreateCalendar() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("reports a refused credential", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		w.provider.RejectCredential(true)
		service := newService(t, w, w.store, newFakeEngine(w.store))

		_, err := service.CreateCalendar(ctx, w.grantee, w.granteeConnection.ID, "Mirror")
		if !errors.Is(err, provider.ErrCredentialRejected) {
			t.Errorf("CreateCalendar() error = %v, want ErrCredentialRejected", err)
		}
	})

	t.Run("refuses another user's connection", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		service := newService(t, w, w.store, newFakeEngine(w.store))

		_, err := service.CreateCalendar(ctx, w.grantee, w.ownerConnection.ID, "Mirror")
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("CreateCalendar() error = %v, want ErrNotFound", err)
		}
	})
}

// mustCalendars lists the calendars the in-memory provider holds for the
// grantee's connection.
func mustCalendars(ctx context.Context, t *testing.T, w *world) []provider.Calendar {
	t.Helper()

	connector, credential, err := newBinder(t, w).Connect(ctx, w.grantee, w.granteeConnection.ID)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	calendars, err := connector.Calendars(ctx, credential)
	if err != nil {
		t.Fatalf("Calendars() error = %v", err)
	}

	return calendars
}
