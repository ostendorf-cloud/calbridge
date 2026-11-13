// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	stdsync "sync"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// memoryProvider is the provider id the in-memory connector registers
// under. No connector exists yet, so the engine is developed against this
// one, shaped like internal/provider's own fake: it obeys the same
// contract, refuses what its capabilities do not declare, and drops what
// it may drop.
const memoryProvider = provider.ID("memory")

// testConnection is the connection every calendar in a test belongs to.
const testConnection = "conn-1"

// fakeCredential names its provider and the connection it was loaded
// from, and carries no secret.
type fakeCredential struct {
	connection string
}

func (c fakeCredential) Provider() provider.ID { return memoryProvider }

func (c fakeCredential) Connection() string { return c.connection }

// fakeCalendar is one calendar the connector serves.
type fakeCalendar struct {
	meta   provider.Calendar
	events map[string]provider.Event
}

// fakeConnector is a calendar provider held in memory. It counts the
// writes it is asked for, which is the only way to tell an identical
// rewrite from no write at all: both leave the target looking the same.
type fakeConnector struct {
	caps provider.Capabilities

	mu           stdsync.Mutex
	calendars    map[string]*fakeCalendar
	nextEvent    int
	nextCalendar int
	creates      int
	updates      int
	deletes      int
}

// newFakeConnector returns a connector declaring the given capabilities
// and holding no calendar yet.
func newFakeConnector(caps provider.Capabilities) *fakeConnector {
	return &fakeConnector{caps: caps, calendars: map[string]*fakeCalendar{}}
}

// addCalendar puts a calendar into the connector and returns its id.
func (c *fakeConnector) addCalendar(name string, meta provider.Calendar) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextCalendar++
	meta.ID = "cal-" + strconv.Itoa(c.nextCalendar)
	meta.Name = name
	c.calendars[meta.ID] = &fakeCalendar{meta: meta, events: map[string]provider.Event{}}

	return meta.ID
}

// counts returns the creates, updates and deletes so far.
func (c *fakeConnector) counts() (creates, updates, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.creates, c.updates, c.deletes
}

// events returns a calendar's events, ordered by id.
func (c *fakeConnector) events(calendarID string) []provider.Event {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return nil
	}

	out := make([]provider.Event, 0, len(cal.events))
	for _, event := range cal.events {
		out = append(out, event)
	}
	slices.SortFunc(out, func(a, b provider.Event) int { return strings.Compare(a.ID, b.ID) })

	return out
}

// holds reports whether a calendar still has an event.
func (c *fakeConnector) holds(calendarID, eventID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return false
	}
	_, held := cal.events[eventID]

	return held
}

// edit changes an event the way a person changes it in their own
// calendar application: through the provider, behind calbridge's back,
// and without touching the counters a test reads to tell writes apart.
func (c *fakeConnector) edit(calendarID, eventID string, change func(*provider.Event)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return
	}
	event, held := cal.events[eventID]
	if !held {
		return
	}
	change(&event)
	cal.events[eventID] = event
}

// put writes an event into a calendar directly, for the events a test
// needs in a source calendar before the engine ever runs.
func (c *fakeConnector) put(calendarID string, event provider.Event) provider.Event {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return provider.Event{}
	}
	if event.ID == "" {
		c.nextEvent++
		event.ID = "evt-" + strconv.Itoa(c.nextEvent)
	}
	cal.events[event.ID] = event

	return event
}

func (c *fakeConnector) ID() provider.ID { return memoryProvider }

func (c *fakeConnector) Capabilities() provider.Capabilities { return c.caps }

func (c *fakeConnector) Calendars(ctx context.Context, cred provider.Credential) ([]provider.Calendar, error) {
	if err := c.begin(ctx, cred); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]provider.Calendar, 0, len(c.calendars))
	for _, cal := range c.calendars {
		out = append(out, cal.meta)
	}
	slices.SortFunc(out, func(a, b provider.Calendar) int { return strings.Compare(a.ID, b.ID) })

	return out, nil
}

func (c *fakeConnector) CreateCalendar(ctx context.Context, cred provider.Credential, name string) (provider.Calendar, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Calendar{}, err
	}
	if !c.caps.CalendarCreation {
		return provider.Calendar{}, fmt.Errorf("provider %q: create calendar: %w", memoryProvider, provider.ErrUnsupported)
	}

	id := c.addCalendar(name, provider.Calendar{TimeZone: "Europe/Berlin", Managed: true})

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.calendars[id].meta, nil
}

func (c *fakeConnector) DeleteCalendar(ctx context.Context, cred provider.Credential, calendarID string) error {
	if err := c.begin(ctx, cred); err != nil {
		return err
	}
	if !c.caps.CalendarCreation {
		return fmt.Errorf("provider %q: delete calendar: %w", memoryProvider, provider.ErrUnsupported)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return nil
	}
	if !cal.meta.Managed {
		return fmt.Errorf("provider %q: calendar %q is not managed by calbridge: %w", memoryProvider, calendarID, provider.ErrUnsupported)
	}
	delete(c.calendars, calendarID)

	return nil
}

func (c *fakeConnector) Changes(ctx context.Context, cred provider.Credential, calendarID string, token provider.SyncToken) (provider.ChangePage, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.ChangePage{}, err
	}
	if !token.IsZero() {
		return provider.ChangePage{}, fmt.Errorf("provider %q: %w", memoryProvider, provider.ErrSyncTokenInvalid)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return provider.ChangePage{}, fmt.Errorf("provider %q: calendar %q: %w", memoryProvider, calendarID, provider.ErrUnknownCalendar)
	}

	ids := make([]string, 0, len(cal.events))
	for id := range cal.events {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	changes := make([]provider.Change, 0, len(ids))
	for _, id := range ids {
		changes = append(changes, provider.Change{Kind: provider.ChangeKindUpsert, EventID: id, Event: cal.events[id]})
	}

	return provider.ChangePage{Changes: changes, State: provider.PageStateComplete}, nil
}

func (c *fakeConnector) CreateEvent(ctx context.Context, cred provider.Credential, calendarID string, event provider.Event) (provider.Event, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Event{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.writable(calendarID)
	if err != nil {
		return provider.Event{}, err
	}
	stored, err := c.accept(event)
	if err != nil {
		return provider.Event{}, err
	}

	c.nextEvent++
	stored.ID = "evt-" + strconv.Itoa(c.nextEvent)
	stored.ETag = "etag-" + strconv.Itoa(c.nextEvent)
	stored.Created = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	stored.Updated = stored.Created
	cal.events[stored.ID] = stored
	c.creates++

	return stored, nil
}

func (c *fakeConnector) UpdateEvent(ctx context.Context, cred provider.Credential, calendarID string, event provider.Event) (provider.Event, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Event{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.writable(calendarID)
	if err != nil {
		return provider.Event{}, err
	}
	held, ok := cal.events[event.ID]
	if !ok {
		return provider.Event{}, fmt.Errorf("provider %q: event %q: %w", memoryProvider, event.ID, provider.ErrUnknownEvent)
	}
	stored, err := c.accept(event)
	if err != nil {
		return provider.Event{}, err
	}

	stored.ETag = held.ETag + "+"
	stored.Created = held.Created
	stored.Updated = held.Updated.Add(time.Minute)
	cal.events[stored.ID] = stored
	c.updates++

	return stored, nil
}

func (c *fakeConnector) DeleteEvent(ctx context.Context, cred provider.Credential, calendarID, eventID string) error {
	if err := c.begin(ctx, cred); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.writable(calendarID)
	if err != nil {
		return err
	}
	if _, held := cal.events[eventID]; !held {
		// Deleting what is already gone succeeds, so an interrupted run is
		// safe to repeat.
		return nil
	}
	delete(cal.events, eventID)
	c.deletes++

	return nil
}

// begin runs the checks every method starts with.
func (c *fakeConnector) begin(ctx context.Context, cred provider.Credential) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("provider %q: %w", memoryProvider, err)
	}

	return provider.CheckCredential(memoryProvider, cred)
}

// writable looks a calendar up for writing. The caller holds the lock.
func (c *fakeConnector) writable(calendarID string) (*fakeCalendar, error) {
	cal, ok := c.calendars[calendarID]
	if !ok {
		return nil, fmt.Errorf("provider %q: calendar %q: %w", memoryProvider, calendarID, provider.ErrUnknownCalendar)
	}
	if cal.meta.ReadOnly {
		return nil, fmt.Errorf("provider %q: calendar %q is read only: %w", memoryProvider, calendarID, provider.ErrUnsupported)
	}

	return cal, nil
}

// accept applies what the capabilities allow to an event on its way in,
// exactly as internal/provider's own fake does: it refuses what the
// provider cannot express and drops what it may drop.
func (c *fakeConnector) accept(event provider.Event) (provider.Event, error) {
	if err := event.Validate(); err != nil {
		return provider.Event{}, fmt.Errorf("provider %q: %w", memoryProvider, err)
	}
	if event.Recurrence.IsMaster() && !c.caps.NativeRecurrence {
		return provider.Event{}, fmt.Errorf("provider %q: recurrence rule: %w", memoryProvider, provider.ErrUnsupported)
	}
	if event.Start.Kind == provider.TimeKindFloating && !c.caps.FloatingTimes {
		return provider.Event{}, fmt.Errorf("provider %q: floating time: %w", memoryProvider, provider.ErrUnsupported)
	}
	if !c.caps.Attendees {
		event.Attendees = nil
	}
	event.Organizer = nil

	return event, nil
}

// scopeKey carries the user a fake transaction is scoped to, the way
// internal/storage/postgres carries its transaction.
type scopeKey struct{}

// fakeStore holds the rows the engine reads and writes, and enforces the
// one rule the real store enforces in Go: a user-scoped call needs a
// scope, and only ever sees its own user's rows.
type fakeStore struct {
	mu        stdsync.Mutex
	calendars map[uuid.UUID]storage.Calendar
	edges     map[uuid.UUID]storage.Edge
	grants    map[uuid.UUID]storage.CalendarGrant
	mappings  map[uuid.UUID]storage.EventMapping
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		calendars: map[uuid.UUID]storage.Calendar{},
		edges:     map[uuid.UUID]storage.Edge{},
		grants:    map[uuid.UUID]storage.CalendarGrant{},
		mappings:  map[uuid.UUID]storage.EventMapping{},
	}
}

// WithUser implements storage.UserScope.
func (s *fakeStore) WithUser(ctx context.Context, userID uuid.UUID, fn func(ctx context.Context) error) error {
	return fn(context.WithValue(ctx, scopeKey{}, userID))
}

// scopeOf reads the scoped user, failing the way the real store fails
// when a caller forgot to open one.
func scopeOf(ctx context.Context) (uuid.UUID, error) {
	userID, ok := ctx.Value(scopeKey{}).(uuid.UUID)
	if !ok {
		return uuid.Nil, storage.ErrNoUserScope
	}

	return userID, nil
}

func (s *fakeStore) Edges() storage.EdgeRepository { return &fakeEdges{store: s} }

func (s *fakeStore) Calendars() storage.CalendarRepository { return &fakeCalendars{store: s} }

func (s *fakeStore) Grants() storage.CalendarGrantRepository { return &fakeGrants{store: s} }

func (s *fakeStore) Mappings() storage.EventMappingRepository { return &fakeMappings{store: s} }

// addCalendar stores a calendar row owned by userID.
func (s *fakeStore) addCalendar(userID uuid.UUID, externalID string, managed bool) storage.Calendar {
	s.mu.Lock()
	defer s.mu.Unlock()

	calendar := storage.Calendar{
		ID:           uuid.New(),
		UserID:       userID,
		ConnectionID: uuid.New(),
		ExternalID:   externalID,
		Name:         externalID,
		Managed:      managed,
	}
	s.calendars[calendar.ID] = calendar

	return calendar
}

// addEdge stores an edge row.
func (s *fakeStore) addEdge(edge storage.Edge) storage.Edge {
	s.mu.Lock()
	defer s.mu.Unlock()

	if edge.ID == uuid.Nil {
		edge.ID = uuid.New()
	}
	s.edges[edge.ID] = edge

	return edge
}

// dropEdge removes an edge row, for a test that wants one edge and not
// the one the harness built.
func (s *fakeStore) dropEdge(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.edges, id)
}

// addGrant stores a grant row.
func (s *fakeStore) addGrant(grant storage.CalendarGrant) storage.CalendarGrant {
	s.mu.Lock()
	defer s.mu.Unlock()

	if grant.ID == uuid.Nil {
		grant.ID = uuid.New()
	}
	s.grants[grant.ID] = grant

	return grant
}

// dropGrant removes a grant row, which is what revoking one does.
func (s *fakeStore) dropGrant(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.grants, id)
}

// mappingsOf returns the mapping rows of one edge, ordered by source
// event id.
func (s *fakeStore) mappingsOf(edgeID uuid.UUID) []storage.EventMapping {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := []storage.EventMapping{}
	for _, mapping := range s.mappings {
		if mapping.EdgeID == edgeID {
			out = append(out, mapping)
		}
	}
	slices.SortFunc(out, func(a, b storage.EventMapping) int { return strings.Compare(a.SourceEventID, b.SourceEventID) })

	return out
}

// hasEdge reports whether the edge row still exists.
func (s *fakeStore) hasEdge(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.edges[id]

	return ok
}

type fakeEdges struct{ store *fakeStore }

func (r *fakeEdges) Create(ctx context.Context, edge storage.Edge) (storage.Edge, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.Edge{}, err
	}
	edge.UserID = userID
	edge.ID = uuid.New()

	return r.store.addEdge(edge), nil
}

func (r *fakeEdges) FindByID(ctx context.Context, id uuid.UUID) (storage.Edge, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.Edge{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	edge, ok := r.store.edges[id]
	if !ok || edge.UserID != userID {
		return storage.Edge{}, storage.ErrNotFound
	}

	return edge, nil
}

func (r *fakeEdges) List(ctx context.Context) ([]storage.Edge, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return nil, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	out := []storage.Edge{}
	for _, edge := range r.store.edges {
		if edge.UserID == userID {
			out = append(out, edge)
		}
	}
	slices.SortFunc(out, func(a, b storage.Edge) int { return strings.Compare(a.ID.String(), b.ID.String()) })

	return out, nil
}

func (r *fakeEdges) ListByCalendar(ctx context.Context, calendarID uuid.UUID) ([]storage.Edge, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}

	out := []storage.Edge{}
	for _, edge := range all {
		if edge.SourceCalendarID == calendarID || edge.TargetCalendarID == calendarID {
			out = append(out, edge)
		}
	}

	return out, nil
}

func (r *fakeEdges) UpdateDetailLevel(ctx context.Context, id uuid.UUID, level storage.DetailLevel) (storage.Edge, error) {
	edge, err := r.FindByID(ctx, id)
	if err != nil {
		return storage.Edge{}, err
	}
	edge.DetailLevel = level

	return r.store.addEdge(edge), nil
}

func (r *fakeEdges) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.FindByID(ctx, id); err != nil {
		return err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	delete(r.store.edges, id)

	return nil
}

type fakeCalendars struct{ store *fakeStore }

func (r *fakeCalendars) Create(ctx context.Context, calendar storage.Calendar) (storage.Calendar, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.Calendar{}, err
	}

	return r.store.addCalendar(userID, calendar.ExternalID, calendar.Managed), nil
}

func (r *fakeCalendars) FindByID(ctx context.Context, id uuid.UUID) (storage.Calendar, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.Calendar{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	calendar, ok := r.store.calendars[id]
	if !ok || calendar.UserID != userID {
		return storage.Calendar{}, storage.ErrNotFound
	}

	return calendar, nil
}

func (r *fakeCalendars) List(ctx context.Context) ([]storage.Calendar, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return nil, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	out := []storage.Calendar{}
	for _, calendar := range r.store.calendars {
		if calendar.UserID == userID {
			out = append(out, calendar)
		}
	}

	return out, nil
}

func (r *fakeCalendars) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.FindByID(ctx, id); err != nil {
		return err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	delete(r.store.calendars, id)

	return nil
}

type fakeGrants struct{ store *fakeStore }

func (r *fakeGrants) Create(ctx context.Context, grant storage.CalendarGrant) (storage.CalendarGrant, error) {
	if _, err := scopeOf(ctx); err != nil {
		return storage.CalendarGrant{}, err
	}

	return r.store.addGrant(grant), nil
}

func (r *fakeGrants) FindByID(ctx context.Context, id uuid.UUID) (storage.CalendarGrant, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.CalendarGrant{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	grant, ok := r.store.grants[id]
	if !ok || (grant.OwnerUserID != userID && grant.GranteeUserID != userID) {
		return storage.CalendarGrant{}, storage.ErrNotFound
	}

	return grant, nil
}

// List returns the grants either side of them can see, matching the row
// level security policy on calendar_grants.
func (r *fakeGrants) List(ctx context.Context) ([]storage.CalendarGrant, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return nil, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	out := []storage.CalendarGrant{}
	for _, grant := range r.store.grants {
		if grant.OwnerUserID == userID || grant.GranteeUserID == userID {
			out = append(out, grant)
		}
	}

	return out, nil
}

func (r *fakeGrants) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.FindByID(ctx, id); err != nil {
		return err
	}
	r.store.dropGrant(id)

	return nil
}

type fakeMappings struct{ store *fakeStore }

func (r *fakeMappings) Create(ctx context.Context, mapping storage.EventMapping) (storage.EventMapping, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for _, held := range r.store.mappings {
		if held.EdgeID == mapping.EdgeID && held.SourceEventID == mapping.SourceEventID {
			return storage.EventMapping{}, storage.ErrConflict
		}
	}

	mapping.ID = uuid.New()
	mapping.UserID = userID
	r.store.mappings[mapping.ID] = mapping

	return mapping, nil
}

func (r *fakeMappings) FindByID(ctx context.Context, id uuid.UUID) (storage.EventMapping, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	mapping, ok := r.store.mappings[id]
	if !ok || mapping.UserID != userID {
		return storage.EventMapping{}, storage.ErrNotFound
	}

	return mapping, nil
}

func (r *fakeMappings) FindByEdgeAndSourceEventID(ctx context.Context, edgeID uuid.UUID, sourceEventID string) (storage.EventMapping, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for _, mapping := range r.store.mappings {
		if mapping.UserID == userID && mapping.EdgeID == edgeID && mapping.SourceEventID == sourceEventID {
			return mapping, nil
		}
	}

	return storage.EventMapping{}, storage.ErrNotFound
}

func (r *fakeMappings) ListByEdge(ctx context.Context, edgeID uuid.UUID) ([]storage.EventMapping, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return nil, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	out := []storage.EventMapping{}
	for _, mapping := range r.store.mappings {
		if mapping.UserID == userID && mapping.EdgeID == edgeID {
			out = append(out, mapping)
		}
	}
	slices.SortFunc(out, func(a, b storage.EventMapping) int { return strings.Compare(a.SourceEventID, b.SourceEventID) })

	return out, nil
}

func (r *fakeMappings) Update(ctx context.Context, mapping storage.EventMapping) (storage.EventMapping, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	held, ok := r.store.mappings[mapping.ID]
	if !ok || held.UserID != userID {
		return storage.EventMapping{}, storage.ErrNotFound
	}

	held.TargetEventID = mapping.TargetEventID
	held.StartsAt = mapping.StartsAt
	held.EndsAt = mapping.EndsAt
	held.ContentHash = mapping.ContentHash
	held.Details = mapping.Details
	r.store.mappings[held.ID] = held

	return held, nil
}

func (r *fakeMappings) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.FindByID(ctx, id); err != nil {
		return err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	delete(r.store.mappings, id)

	return nil
}

func (r *fakeMappings) DeleteByEdge(ctx context.Context, edgeID uuid.UUID) (int64, error) {
	userID, err := scopeOf(ctx)
	if err != nil {
		return 0, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	removed := int64(0)
	for id, mapping := range r.store.mappings {
		if mapping.UserID == userID && mapping.EdgeID == edgeID {
			delete(r.store.mappings, id)
			removed++
		}
	}

	return removed, nil
}

// fakeBinder resolves the edges a test built to the endpoints it set up.
type fakeBinder struct {
	bindings map[uuid.UUID]sync.Binding
	err      error
}

func newFakeBinder() *fakeBinder { return &fakeBinder{bindings: map[uuid.UUID]sync.Binding{}} }

func (b *fakeBinder) Bind(_ context.Context, edge storage.Edge) (sync.Binding, error) {
	if b.err != nil {
		return sync.Binding{}, b.err
	}

	binding, ok := b.bindings[edge.ID]
	if !ok {
		return sync.Binding{}, fmt.Errorf("no binding for edge %s", edge.ID)
	}
	binding.Edge = edge

	return binding, nil
}

func (b *fakeBinder) add(binding sync.Binding) { b.bindings[binding.Edge.ID] = binding }
