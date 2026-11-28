// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package mirror runs the sync engine inside the service. internal/sync
// decides what an edge copies and how an edge is taken down. This package
// decides when that happens, and it holds what the engine cannot: a
// credential decrypted next to its connector (Binder), the trigger that
// makes a reconcile run without a person asking for it, and the record of
// which edges the provider has turned away.
//
// # What triggers a pass
//
// Three things, in order of how much the system relies on them.
//
// A schedule is the floor. Every Interval, Run reconciles every edge of
// every user who owns one. It depends on no notification having arrived,
// which is the property internal/sync's full reconcile exists to give.
//
// Creating an edge queues a pass over that edge, so a person drawing one
// does not wait a whole interval to see it work.
//
// Revoking a grant queues sync.Engine.EnforceGrants, which tears down the
// edges the grant covered. Those edges belong to the grantee and sit
// outside the owner's scope, so no API handler could do it. The schedule
// repeats the check, so a queue that was full or a process that stopped
// before the job ran loses nothing.
//
// Deleting an edge is the one trigger that runs inside the request,
// because the person asked for the copies to be taken back and is owed the
// answer whether they were.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	stdsync "sync"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// ErrIncomplete reports a binder or a service built without a dependency
// it cannot work without. It is returned at construction so a service
// that would start and do nothing never starts.
var ErrIncomplete = errors.New("incomplete mirror options")

const (
	// queueSize bounds the triggers waiting for the worker. A full queue
	// drops the trigger and logs it, because the scheduled pass covers
	// whatever the trigger would have.
	queueSize = 64
	// edgeTimeout bounds one edge's pass, so one provider that never
	// answers cannot hold every other edge behind it.
	edgeTimeout = 5 * time.Minute
)

// Engine is the part of internal/sync the service drives.
type Engine interface {
	Coverage(ctx context.Context, edge storage.Edge) (sync.Coverage, error)
	Reconcile(ctx context.Context, b sync.Binding) (sync.Report, error)
	DeleteEdge(ctx context.Context, b sync.Binding) (sync.Report, error)
	EnforceGrants(ctx context.Context, grant storage.CalendarGrant) (sync.Report, error)
}

// Store is what the service reads outside the engine. ListEdgeOwners is
// the one call that needs no user scope: it is how a scheduled pass learns
// whose edges to run before it can scope to any of them.
type Store interface {
	BinderStore

	Edges() storage.EdgeRepository
	ListEdgeOwners(ctx context.Context) ([]uuid.UUID, error)
}

// Options are the service's dependencies. Every field but Logger is
// required.
type Options struct {
	Store  Store
	Engine Engine
	// Binder is the one the engine was built with. The service uses it for
	// the calls the engine does not make itself, such as creating the
	// calendar an edge will write into.
	Binder *Binder
	// Interval is how often the scheduled pass runs.
	Interval time.Duration
	// Logger receives what a pass survives. Nil discards it.
	Logger *slog.Logger
}

// Service runs the engine for every user and remembers which edges the
// provider has refused.
type Service struct {
	store    Store
	engine   Engine
	binder   *Binder
	interval time.Duration
	log      *slog.Logger

	jobs  chan job
	locks stdsync.Map

	mu      stdsync.Mutex
	refused map[uuid.UUID]struct{}
}

// job is one queued trigger: a grant to enforce, or one edge to reconcile.
type job struct {
	grant  *storage.CalendarGrant
	userID uuid.UUID
	edgeID uuid.UUID
}

// New builds the service. Without a store, an engine, a binder and a
// positive interval it returns ErrIncomplete, so a service that would
// start and do nothing cannot be built.
func New(opts Options) (*Service, error) {
	var missing []string

	if opts.Store == nil {
		missing = append(missing, "store")
	}
	if opts.Engine == nil {
		missing = append(missing, "engine")
	}
	if opts.Binder == nil {
		missing = append(missing, "binder")
	}
	if opts.Interval <= 0 {
		missing = append(missing, "interval")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("mirror is missing %v: %w", missing, ErrIncomplete)
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Service{
		store:    opts.Store,
		engine:   opts.Engine,
		binder:   opts.Binder,
		interval: opts.Interval,
		log:      logger,
		jobs:     make(chan job, queueSize),
		refused:  map[uuid.UUID]struct{}{},
	}, nil
}

// Run works until ctx ends: one full pass at start, then one every
// interval, with the queued triggers handled between them. It returns nil
// when ctx ends.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.Pass(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.Pass(ctx)
		case queued := <-s.jobs:
			s.handle(ctx, queued)
		}
	}
}

// Pass reconciles every edge of every user who owns one. A failure on one
// edge is logged and does not stop the rest.
func (s *Service) Pass(ctx context.Context) {
	owners, err := s.store.ListEdgeOwners(ctx)
	if err != nil {
		s.log.Error("list the users who own an edge", "error", err)

		return
	}

	for _, userID := range owners {
		if ctx.Err() != nil {
			return
		}

		s.passUser(ctx, userID)
	}
}

// EdgeCreated queues a pass over one new edge. It never blocks.
func (s *Service) EdgeCreated(userID, edgeID uuid.UUID) {
	s.enqueue(job{userID: userID, edgeID: edgeID})
}

// GrantRevoked queues the teardown of the edges a revoked grant covered.
// It takes the grant row, because the grantee is the only scope that can
// see those edges. It never blocks.
func (s *Service) GrantRevoked(grant storage.CalendarGrant) {
	s.enqueue(job{grant: &grant})
}

func (s *Service) enqueue(queued job) {
	select {
	case s.jobs <- queued:
	default:
		s.log.Warn("the trigger queue is full, the scheduled pass will catch up")
	}
}

func (s *Service) handle(ctx context.Context, queued job) {
	if queued.grant != nil {
		s.enforceGrant(ctx, *queued.grant)

		return
	}

	unlock, err := s.lock(ctx, queued.userID)
	if err != nil {
		// Only the end of ctx stops the wait, and the scheduled pass
		// covers the trigger.
		return
	}
	defer unlock()

	var edge storage.Edge

	if err := s.store.WithUser(ctx, queued.userID, func(ctx context.Context) error {
		found, err := s.store.Edges().FindByID(ctx, queued.edgeID)
		if err != nil {
			return err
		}
		edge = found

		return nil
	}); err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			s.log.Error("read a new edge", "edge", queued.edgeID, "error", err)
		}

		return
	}

	if err := s.runEdge(ctx, edge); err != nil {
		s.log.Warn("pass over a new edge failed", "edge", edge.ID, "reason", reason(err))
	}
}

// enforceGrant tears down what a revoked grant covered and then collects
// the calendars that teardown left without an edge.
func (s *Service) enforceGrant(ctx context.Context, grant storage.CalendarGrant) {
	unlock, err := s.lock(ctx, grant.GranteeUserID)
	if err != nil {
		// Only the end of ctx stops the wait, so the service is stopping.
		return
	}
	defer unlock()

	report, err := s.engine.EnforceGrants(ctx, grant)
	if err != nil {
		s.log.Warn("enforce a revoked grant", "grant", grant.ID, "reason", reason(err))
	} else {
		s.log.Info("enforced a revoked grant", "grant", grant.ID, "operations", len(report.Operations))
	}

	s.collect(ctx, grant.GranteeUserID)
}

// passUser runs every edge of one user.
func (s *Service) passUser(ctx context.Context, userID uuid.UUID) {
	unlock, err := s.lock(ctx, userID)
	if err != nil {
		// Only the end of ctx stops the wait, so the service is stopping.
		return
	}
	defer unlock()

	var edges []storage.Edge

	if err := s.store.WithUser(ctx, userID, func(ctx context.Context) error {
		found, err := s.store.Edges().List(ctx)
		if err != nil {
			return err
		}
		edges = found

		return nil
	}); err != nil {
		s.log.Error("list the edges of a user", "user", userID, "error", err)

		return
	}

	for _, edge := range edges {
		if ctx.Err() != nil {
			return
		}

		if err := s.runEdge(ctx, edge); err != nil {
			s.log.Warn("pass over an edge failed", "edge", edge.ID, "user", userID, "reason", reason(err))
		}
	}

	s.collect(ctx, userID)
}

// runEdge reconciles one edge, or tears it down where no grant covers it
// any more, and records whether the provider refused the credential. The
// caller holds the user's lock.
//
// The decision whether a grant still covers the edge is the engine's own
// (sync.CheckGrants), and so is the teardown. Calling sync.Engine.Run
// would do the same, but its error does not say which edge a refused
// credential belonged to, and the person has to be told.
func (s *Service) runEdge(ctx context.Context, edge storage.Edge) error {
	ctx, cancel := context.WithTimeout(ctx, edgeTimeout)
	defer cancel()

	coverage, err := s.engine.Coverage(ctx, edge)
	if err != nil {
		return err
	}

	if err := sync.CheckGrants(edge, coverage); err != nil {
		if !errors.Is(err, sync.ErrGrantMissing) && !errors.Is(err, sync.ErrGrantExceeded) {
			return err
		}

		return s.tearDownUncovered(ctx, edge)
	}

	binding, err := s.binder.Bind(ctx, edge)
	if err != nil {
		return err
	}

	_, err = s.engine.Reconcile(ctx, binding)
	s.note(edge.ID, err)

	return err
}

// tearDownUncovered takes down an edge no grant covers. The engine's
// teardown of that case is EnforceGrants, and it reads only the grantee
// and the calendar of the grant it is given, so the edge's own two
// calendars stand in for the row that no longer exists.
func (s *Service) tearDownUncovered(ctx context.Context, edge storage.Edge) error {
	var errs []error

	for _, calendarID := range []uuid.UUID{edge.SourceCalendarID, edge.TargetCalendarID} {
		_, err := s.engine.EnforceGrants(ctx, storage.CalendarGrant{
			GranteeUserID: edge.UserID,
			CalendarID:    calendarID,
		})
		if err != nil {
			errs = append(errs, err)
		}
	}

	err := errors.Join(errs...)
	s.note(edge.ID, err)

	return err
}

// DeleteEdge takes back everything the edge wrote and then removes the
// edge. It returns the engine's error when the copies could not be
// removed, and the edge stays, so a refused credential is reported and
// nothing is left behind unowned.
func (s *Service) DeleteEdge(ctx context.Context, userID, edgeID uuid.UUID) error {
	unlock, err := s.lock(ctx, userID)
	if err != nil {
		return err
	}
	defer unlock()

	var edge storage.Edge

	if err := s.store.WithUser(ctx, userID, func(ctx context.Context) error {
		found, err := s.store.Edges().FindByID(ctx, edgeID)
		if err != nil {
			return err
		}
		edge = found

		return nil
	}); err != nil {
		return fmt.Errorf("find edge %s: %w", edgeID, err)
	}

	binding, err := s.binder.Bind(ctx, edge)
	if err != nil {
		return err
	}

	_, err = s.engine.DeleteEdge(ctx, binding)
	if errors.Is(err, sync.ErrInvalidBinding) && binding.Source.Connector == nil {
		// The source is gone because its grant was revoked. The engine
		// refuses to delete an edge it cannot read, and its teardown for
		// exactly this case does not need the source.
		err = s.tearDownUncovered(ctx, edge)
	}

	s.note(edge.ID, err)

	if err != nil {
		return fmt.Errorf("delete edge %s: %w", edgeID, err)
	}

	s.forget(edge.ID)
	s.collect(ctx, userID)

	return nil
}

// CreateCalendar creates a calendar at the provider, in a connection the
// user owns, and returns the provider's own id for it. It runs before any
// row exists, outside any database transaction, so the row can be written
// once with the real id.
func (s *Service) CreateCalendar(ctx context.Context, userID, connectionID uuid.UUID, name string) (string, error) {
	connector, credential, err := s.binder.Connect(ctx, userID, connectionID)
	if err != nil {
		return "", err
	}

	if !connector.Capabilities().CalendarCreation {
		return "", fmt.Errorf("provider %q creates no calendar: %w", connector.ID(), provider.ErrUnsupported)
	}

	created, err := connector.CreateCalendar(ctx, credential, name)
	if err != nil {
		return "", fmt.Errorf("create calendar at the provider: %w", err)
	}

	return created.ID, nil
}

// DiscardCalendar removes a calendar CreateCalendar made, for the case
// where the request that wanted it failed afterwards.
func (s *Service) DiscardCalendar(ctx context.Context, userID, connectionID uuid.UUID, externalID string) error {
	connector, credential, err := s.binder.Connect(ctx, userID, connectionID)
	if err != nil {
		return err
	}

	if err := connector.DeleteCalendar(ctx, credential, externalID); err != nil {
		return fmt.Errorf("delete calendar at the provider: %w", err)
	}

	return nil
}

// CredentialRefused reports whether the provider turned away the
// credential behind this edge on its last pass. It is held in memory: a
// restart forgets it until the next pass finds it out again.
func (s *Service) CredentialRefused(edgeID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, refused := s.refused[edgeID]

	return refused
}

// note records the outcome of a pass over one edge. A refusal sets the
// flag, a clean pass clears it, and any other failure leaves it alone,
// since it says nothing about the credential.
func (s *Service) note(edgeID uuid.UUID, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case errors.Is(err, provider.ErrCredentialRejected):
		s.refused[edgeID] = struct{}{}
	case err == nil:
		delete(s.refused, edgeID)
	}
}

func (s *Service) forget(edgeID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.refused, edgeID)
}

// lock serialises everything that touches one user's edges. The engine
// leaves two passes over one edge to its caller, and a scheduled pass, a
// queued trigger and a request can otherwise overlap.
//
// The lock is a one slot channel, not a mutex, so a caller waiting behind a
// long pass stops waiting when its context ends. A request with a deadline
// then answers in time instead of queueing behind work it cannot cancel.
func (s *Service) lock(ctx context.Context, userID uuid.UUID) (func(), error) {
	held, _ := s.locks.LoadOrStore(userID, make(chan struct{}, 1))

	slot, _ := held.(chan struct{})

	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for the lock of user %s: %w", userID, ctx.Err())
	}
}

// collect deletes the calendars calbridge created for a user that no edge
// of theirs names any more. The engine removes such a calendar at the
// provider when its edge goes, and the row naming it stays behind, since
// the engine does not own that table. A calendar something else still
// references, such as a grant or another user's edge, refuses the delete
// and is left for the next pass.
func (s *Service) collect(ctx context.Context, userID uuid.UUID) {
	err := s.store.WithUser(ctx, userID, func(ctx context.Context) error {
		edges, err := s.store.Edges().List(ctx)
		if err != nil {
			return err
		}

		named := make(map[uuid.UUID]struct{}, 2*len(edges))
		for _, edge := range edges {
			named[edge.SourceCalendarID] = struct{}{}
			named[edge.TargetCalendarID] = struct{}{}
		}

		calendars, err := s.store.Calendars().List(ctx)
		if err != nil {
			return err
		}

		for _, calendar := range calendars {
			if _, ok := named[calendar.ID]; ok || !calendar.Managed {
				continue
			}

			if err := s.store.Calendars().Delete(ctx, calendar.ID); err != nil {
				s.log.Debug("keep a calendar something still references", "calendar", calendar.ID, "error", err)
			}
		}

		return nil
	})
	if err != nil {
		s.log.Warn("collect the calendars no edge names", "user", userID, "error", err)
	}
}
