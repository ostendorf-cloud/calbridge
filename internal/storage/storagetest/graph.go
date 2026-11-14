// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package storagetest

import (
	"context"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// This file holds the sync graph half of the fake: calendars, edges and
// grants. It imitates three separate things the real schema does, and
// the distinction matters when reading a test that depends on one of
// them:
//
//   - The row level security policies, by filtering every read and every
//     write to the scope the transaction carries, exactly as
//     store.go's connections and identities already do.
//   - calendar_grants' own two-sided policy: a grant is visible to its
//     owner and to its grantee, and only the owner may create one.
//   - The edges_check_calendar_access trigger: an edge may only name a
//     calendar its own user owns or holds a grant over, and a calendar
//     that does not exist and one nobody shared are refused identically.
//
// It is an imitation, not a second authority. The database is what
// decides these in production, and internal/storage/postgres's
// integration test is what proves it does; a test using this fake is
// testing the layer above, not the rule.
//
// One thing it does not imitate: WithUser here is not a transaction, so
// a sequence of writes that fails halfway leaves the earlier ones
// standing, where the real store would roll them back. A test about
// what a failed multi-step write leaves behind belongs in the
// integration suite rather than here.

// Calendars returns the calendar repository.
func (s *Store) Calendars() storage.CalendarRepository { return &calendars{store: s} }

// Edges returns the edge repository.
func (s *Store) Edges() storage.EdgeRepository { return &edges{store: s} }

// CalendarGrants returns the calendar grant repository.
func (s *Store) CalendarGrants() storage.CalendarGrantRepository {
	return &calendarGrants{store: s}
}

// Sharing returns the sharing repository.
func (s *Store) Sharing() storage.SharingRepository { return &sharing{store: s} }

// CountCalendars reports how many calendars are stored.
func (s *Store) CountCalendars() int { return s.count(func() int { return len(s.calendars) }) }

// CountEdges reports how many edges are stored.
func (s *Store) CountEdges() int { return s.count(func() int { return len(s.edges) }) }

// CountCalendarGrants reports how many grants are stored.
func (s *Store) CountCalendarGrants() int { return s.count(func() int { return len(s.grants) }) }

// Edge returns the stored row for id, for a test asserting on what a
// call left behind.
func (s *Store) Edge(id uuid.UUID) (storage.Edge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	edge, ok := s.edges[id]

	return edge, ok
}

// grantsCalendarTo reports whether a grant lets granteeID reach
// calendarID. The caller already holds the lock.
func (s *Store) grantsCalendarTo(calendarID, granteeID uuid.UUID) bool {
	for _, grant := range s.grants {
		if grant.CalendarID == calendarID && grant.GranteeUserID == granteeID {
			return true
		}
	}

	return false
}

// reachesCalendar reports whether userID owns calendarID outright or
// holds a grant over it, which is exactly what
// edges_check_calendar_access asks. The caller already holds the lock.
func (s *Store) reachesCalendar(calendarID, userID uuid.UUID) bool {
	if calendar, ok := s.calendars[calendarID]; ok && calendar.UserID == userID {
		return true
	}

	return s.grantsCalendarTo(calendarID, userID)
}

type calendars struct{ store *Store }

// Create implements storage.CalendarRepository.
func (r *calendars) Create(_ context.Context, calendar storage.Calendar) (storage.Calendar, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Calendar{}, err
	}

	if calendar.UserID != scope {
		return storage.Calendar{}, storage.ErrNotFound
	}

	connection, ok := r.store.connections[calendar.ConnectionID]
	if !ok || connection.UserID != scope {
		// calendars_user_id_connection_id_fkey: a calendar is tied to a
		// connection the same user owns, and a connection that is not
		// theirs is refused the same way one that does not exist is.
		return storage.Calendar{}, storage.ErrNotFound
	}

	for _, existing := range r.store.calendars {
		if existing.ConnectionID == calendar.ConnectionID && existing.ExternalID == calendar.ExternalID {
			return storage.Calendar{}, storage.ErrConflict
		}
	}

	calendar.ID = uuid.New()
	calendar.CreatedAt = fixedTime
	calendar.UpdatedAt = fixedTime
	r.store.calendars[calendar.ID] = calendar
	r.store.calendarOrder = append(r.store.calendarOrder, calendar.ID)

	return calendar, nil
}

// FindByID implements storage.CalendarRepository. Ownership only: a
// calendar shared with this user comes back through ListSharedWithMe,
// with the grant's bound attached, and never through here.
func (r *calendars) FindByID(_ context.Context, id uuid.UUID) (storage.Calendar, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Calendar{}, err
	}

	calendar, ok := r.store.calendars[id]
	if !ok || calendar.UserID != scope {
		return storage.Calendar{}, storage.ErrNotFound
	}

	return calendar, nil
}

// List implements storage.CalendarRepository.
func (r *calendars) List(_ context.Context) ([]storage.Calendar, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	var found []storage.Calendar

	for _, id := range r.store.calendarOrder {
		if calendar, ok := r.store.calendars[id]; ok && calendar.UserID == scope {
			found = append(found, calendar)
		}
	}

	return found, nil
}

// Delete implements storage.CalendarRepository.
func (r *calendars) Delete(_ context.Context, id uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return err
	}

	calendar, ok := r.store.calendars[id]
	if !ok || calendar.UserID != scope {
		return storage.ErrNotFound
	}

	delete(r.store.calendars, id)

	return nil
}

type edges struct{ store *Store }

// Create implements storage.EdgeRepository. The two reachability checks
// are the fake's stand-in for edges_check_calendar_access; see this
// file's own comment for what that does and does not prove.
func (r *edges) Create(_ context.Context, edge storage.Edge) (storage.Edge, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Edge{}, err
	}

	if edge.UserID != scope {
		return storage.Edge{}, storage.ErrNotFound
	}

	if !r.store.reachesCalendar(edge.SourceCalendarID, scope) ||
		!r.store.reachesCalendar(edge.TargetCalendarID, scope) {
		return storage.Edge{}, storage.ErrNotFound
	}

	edge.ID = uuid.New()
	edge.CreatedAt = fixedTime
	edge.UpdatedAt = fixedTime
	r.store.edges[edge.ID] = edge
	r.store.edgeOrder = append(r.store.edgeOrder, edge.ID)

	return edge, nil
}

// FindByID implements storage.EdgeRepository.
func (r *edges) FindByID(_ context.Context, id uuid.UUID) (storage.Edge, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Edge{}, err
	}

	edge, ok := r.store.edges[id]
	if !ok || edge.UserID != scope {
		return storage.Edge{}, storage.ErrNotFound
	}

	return edge, nil
}

// List implements storage.EdgeRepository.
func (r *edges) List(_ context.Context) ([]storage.Edge, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	var found []storage.Edge

	for _, id := range r.store.edgeOrder {
		if edge, ok := r.store.edges[id]; ok && edge.UserID == scope {
			found = append(found, edge)
		}
	}

	return found, nil
}

// ListByCalendar implements storage.EdgeRepository.
func (r *edges) ListByCalendar(ctx context.Context, calendarID uuid.UUID) ([]storage.Edge, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}

	var found []storage.Edge

	for _, edge := range all {
		if edge.SourceCalendarID == calendarID || edge.TargetCalendarID == calendarID {
			found = append(found, edge)
		}
	}

	return found, nil
}

// UpdateDetailLevel implements storage.EdgeRepository.
func (r *edges) UpdateDetailLevel(
	_ context.Context,
	id uuid.UUID,
	detailLevel storage.DetailLevel,
) (storage.Edge, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Edge{}, err
	}

	edge, ok := r.store.edges[id]
	if !ok || edge.UserID != scope {
		return storage.Edge{}, storage.ErrNotFound
	}

	edge.DetailLevel = detailLevel
	edge.UpdatedAt = fixedTime
	r.store.edges[id] = edge

	return edge, nil
}

// Delete implements storage.EdgeRepository.
func (r *edges) Delete(_ context.Context, id uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return err
	}

	edge, ok := r.store.edges[id]
	if !ok || edge.UserID != scope {
		return storage.ErrNotFound
	}

	delete(r.store.edges, id)

	return nil
}

type calendarGrants struct{ store *Store }

// Create implements storage.CalendarGrantRepository. Only the owner of
// the calendar may create a grant over it, matching the WITH CHECK half
// of calendar_grants' own policy and the composite foreign key that ties
// the grant to a calendar the stated owner really owns.
func (r *calendarGrants) Create(
	_ context.Context,
	grant storage.CalendarGrant,
) (storage.CalendarGrant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.CalendarGrant{}, err
	}

	if grant.OwnerUserID != scope {
		return storage.CalendarGrant{}, storage.ErrNotFound
	}

	calendar, ok := r.store.calendars[grant.CalendarID]
	if !ok || calendar.UserID != scope {
		return storage.CalendarGrant{}, storage.ErrNotFound
	}

	if grant.OwnerUserID == grant.GranteeUserID {
		// calendar_grants_owner_grantee_distinct.
		return storage.CalendarGrant{}, storage.ErrConflict
	}

	for _, existing := range r.store.grants {
		if existing.OwnerUserID == grant.OwnerUserID &&
			existing.GranteeUserID == grant.GranteeUserID &&
			existing.CalendarID == grant.CalendarID {
			return storage.CalendarGrant{}, storage.ErrConflict
		}
	}

	grant.ID = uuid.New()
	grant.CreatedAt = fixedTime
	r.store.grants[grant.ID] = grant
	r.store.grantOrder = append(r.store.grantOrder, grant.ID)

	return grant, nil
}

// FindByID implements storage.CalendarGrantRepository. Either side of
// the grant may read it.
func (r *calendarGrants) FindByID(
	_ context.Context,
	id uuid.UUID,
) (storage.CalendarGrant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.CalendarGrant{}, err
	}

	grant, ok := r.store.grants[id]
	if !ok || (grant.OwnerUserID != scope && grant.GranteeUserID != scope) {
		return storage.CalendarGrant{}, storage.ErrNotFound
	}

	return grant, nil
}

// List implements storage.CalendarGrantRepository.
func (r *calendarGrants) List(_ context.Context) ([]storage.CalendarGrant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	var found []storage.CalendarGrant

	for _, id := range r.store.grantOrder {
		grant, ok := r.store.grants[id]
		if ok && (grant.OwnerUserID == scope || grant.GranteeUserID == scope) {
			found = append(found, grant)
		}
	}

	return found, nil
}

// Delete implements storage.CalendarGrantRepository. Either side may
// remove a grant, matching the policy's USING clause.
func (r *calendarGrants) Delete(_ context.Context, id uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return err
	}

	grant, ok := r.store.grants[id]
	if !ok || (grant.OwnerUserID != scope && grant.GranteeUserID != scope) {
		return storage.ErrNotFound
	}

	delete(r.store.grants, id)

	return nil
}
