// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// fakeID is the provider id the in-memory connector registers under. A
// real connector exports its own id from its own package, so this
// package names no provider.
const fakeID = provider.ID("memory")

// Token forms the in-memory connector hands out. A provider's token is
// opaque to everything above the connector, so the shape here is this
// connector's business alone. Two forms are needed because the two
// positions a provider can be in are genuinely different: a place in the
// change log, which survives a restart, and a cursor into an initial
// snapshot, which does not.
const (
	logPrefix      = "log:"
	snapshotPrefix = "snapshot:"
)

// fakeCredential is the shape a connector's own credential type takes: it
// names its provider and the connection it was loaded from, and carries
// no secret into this package.
type fakeCredential struct {
	provider   provider.ID
	connection string
}

func (c fakeCredential) Provider() provider.ID { return c.provider }

func (c fakeCredential) Connection() string { return c.connection }

// defaultConnection is the connection a new connector's first calendar
// belongs to.
const defaultConnection = "conn-1"

// credentialFor returns a credential for the in-memory connector, bound
// to one connection. One connector serves every connection of its
// provider, and the credential is what picks the account.
func credentialFor(connection string) fakeCredential {
	return fakeCredential{provider: fakeID, connection: connection}
}

// fakeCalendar is one calendar the connector serves: the connection it
// belongs to, the events it holds now, and the log of every change made
// to it since it was created.
type fakeCalendar struct {
	connection string
	meta       provider.Calendar
	events     map[string]provider.Event
	log        []provider.Change
}

// snapshot returns the calendar's current events as upserts, ordered by
// id so a paged initial sync hands them over in a stable order.
func (f *fakeCalendar) snapshot() []provider.Change {
	ids := make([]string, 0, len(f.events))
	for id := range f.events {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]provider.Change, 0, len(ids))
	for _, id := range ids {
		out = append(out, provider.Change{
			Kind:    provider.ChangeKindUpsert,
			EventID: id,
			Event:   f.events[id],
		})
	}

	return out
}

// fakeConnector is a calendar provider held in memory, and the worked
// example a real connector is written against. It makes no network call,
// so a test of the contract tests the contract and nothing else.
//
// It behaves the way this package's documentation says a connector
// behaves: it refuses a credential meant for another provider, refuses an
// operation its capabilities do not declare, pages a change read, and
// rejects a token it has invalidated.
type fakeConnector struct {
	id   provider.ID
	caps provider.Capabilities

	// pageSize is how many changes one read hands over. A larger run
	// comes back over several pages.
	pageSize int
	// incrementalState is what the connector reports while pages of an
	// incremental read remain. A provider whose mid-run token is a real
	// position reports PageStateResumable, one whose token only continues
	// the run reports PageStateContinued. A paged initial sync is always
	// PageStateContinued, because a snapshot cursor is worth nothing
	// after the run that opened it.
	incrementalState provider.PageState

	mu            sync.Mutex
	calendars     map[string]*fakeCalendar
	invalidTokens map[string]bool
	nextEventID   int
	nextCalendar  int
}

// newFakeConnector returns a connector declaring the given capabilities,
// holding one writable calendar named "Primary" on the connection
// defaultConnection names.
func newFakeConnector(caps provider.Capabilities) *fakeConnector {
	c := &fakeConnector{
		id:               fakeID,
		caps:             caps,
		pageSize:         100,
		incrementalState: provider.PageStateResumable,
		calendars:        map[string]*fakeCalendar{},
		invalidTokens:    map[string]bool{},
	}
	c.addCalendar(defaultConnection, provider.Calendar{Name: "Primary", TimeZone: "Europe/Berlin", Primary: true})

	return c
}

// addCalendar puts a calendar on a connection and returns it with its id
// filled in.
func (c *fakeConnector) addCalendar(connection string, meta provider.Calendar) provider.Calendar {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.store(connection, meta)
}

// store puts a calendar into the connector. The caller holds the lock.
func (c *fakeConnector) store(connection string, meta provider.Calendar) provider.Calendar {
	c.nextCalendar++
	meta.ID = "cal-" + strconv.Itoa(c.nextCalendar)
	c.calendars[meta.ID] = &fakeCalendar{
		connection: connection,
		meta:       meta,
		events:     map[string]provider.Event{},
	}

	return meta
}

// invalidate makes the connector reject the token from now on, which is
// how a provider that expired a position behaves.
func (c *fakeConnector) invalidate(token provider.SyncToken) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.invalidTokens[token.Value] = true
}

func (c *fakeConnector) ID() provider.ID { return c.id }

func (c *fakeConnector) Capabilities() provider.Capabilities { return c.caps }

func (c *fakeConnector) Calendars(ctx context.Context, cred provider.Credential) ([]provider.Calendar, error) {
	if err := c.begin(ctx, cred); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]provider.Calendar, 0, len(c.calendars))
	for _, cal := range c.calendars {
		if cal.connection == cred.Connection() {
			out = append(out, cal.meta)
		}
	}
	slices.SortFunc(out, func(a, b provider.Calendar) int { return strings.Compare(a.ID, b.ID) })

	return out, nil
}

func (c *fakeConnector) CreateCalendar(ctx context.Context, cred provider.Credential, name string) (provider.Calendar, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Calendar{}, err
	}
	if !c.caps.CalendarCreation {
		return provider.Calendar{}, fmt.Errorf("provider %q: create calendar: %w", c.id, provider.ErrUnsupported)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.store(cred.Connection(), provider.Calendar{Name: name, TimeZone: "Europe/Berlin", Managed: true}), nil
}

func (c *fakeConnector) DeleteCalendar(ctx context.Context, cred provider.Credential, calendarID string) error {
	if err := c.begin(ctx, cred); err != nil {
		return err
	}
	if !c.caps.CalendarCreation {
		return fmt.Errorf("provider %q: delete calendar: %w", c.id, provider.ErrUnsupported)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok || cal.connection != cred.Connection() {
		// Deleting what is already gone succeeds, so a run interrupted
		// after the provider acted is safe to repeat, the same as
		// DeleteEvent.
		return nil
	}
	if !cal.meta.Managed {
		return fmt.Errorf("provider %q: calendar %q is not managed by calbridge: %w", c.id, calendarID, provider.ErrUnsupported)
	}

	delete(c.calendars, calendarID)

	return nil
}

func (c *fakeConnector) Changes(ctx context.Context, cred provider.Credential, calendarID string, token provider.SyncToken) (provider.ChangePage, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.ChangePage{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.read(cred, calendarID)
	if err != nil {
		return provider.ChangePage{}, err
	}

	return c.page(cal, token)
}

// page turns a token into one page of changes. The zero token starts an
// initial sync, which reports every event the calendar holds now and no
// deletion at all. The caller holds the lock.
func (c *fakeConnector) page(cal *fakeCalendar, token provider.SyncToken) (provider.ChangePage, error) {
	if c.invalidTokens[token.Value] && !token.IsZero() {
		return provider.ChangePage{}, fmt.Errorf("provider %q: %w", c.id, provider.ErrSyncTokenInvalid)
	}

	pending, cursor, state, err := c.pending(cal, token)
	if err != nil {
		return provider.ChangePage{}, err
	}

	if len(pending) <= c.pageSize {
		return provider.ChangePage{
			Changes: pending,
			Token:   provider.SyncToken{Value: logPrefix + strconv.Itoa(len(cal.log))},
			State:   provider.PageStateComplete,
		}, nil
	}

	return provider.ChangePage{
		Changes: pending[:c.pageSize],
		Token:   provider.SyncToken{Value: cursor(c.pageSize)},
		State:   state,
	}, nil
}

// pending returns the changes the token has not seen, a function naming
// the token that continues from an offset into them, and the state that
// goes with such a continuation.
func (c *fakeConnector) pending(cal *fakeCalendar, token provider.SyncToken) (changes []provider.Change, cursor func(int) string, state provider.PageState, err error) {
	switch {
	case token.IsZero():
		return cal.snapshot(),
			func(offset int) string { return snapshotPrefix + strconv.Itoa(offset) },
			provider.PageStateContinued,
			nil

	case strings.HasPrefix(token.Value, snapshotPrefix):
		from, convErr := position(token, snapshotPrefix)
		snapshot := cal.snapshot()
		if convErr != nil || from > len(snapshot) {
			return nil, nil, provider.PageStateUnset, fmt.Errorf("provider %q: %w", c.id, provider.ErrSyncTokenInvalid)
		}

		return snapshot[from:],
			func(offset int) string { return snapshotPrefix + strconv.Itoa(from+offset) },
			provider.PageStateContinued,
			nil

	case strings.HasPrefix(token.Value, logPrefix):
		from, convErr := position(token, logPrefix)
		if convErr != nil || from > len(cal.log) {
			return nil, nil, provider.PageStateUnset, fmt.Errorf("provider %q: %w", c.id, provider.ErrSyncTokenInvalid)
		}

		return slices.Clone(cal.log[from:]),
			func(offset int) string { return logPrefix + strconv.Itoa(from+offset) },
			c.incrementalState,
			nil

	default:
		return nil, nil, provider.PageStateUnset, fmt.Errorf("provider %q: %w", c.id, provider.ErrSyncTokenInvalid)
	}
}

// position reads the offset out of one of this connector's tokens.
func position(token provider.SyncToken, prefix string) (int, error) {
	at, err := strconv.Atoi(strings.TrimPrefix(token.Value, prefix))
	if err != nil {
		return 0, fmt.Errorf("token %q: %w", token.Value, err)
	}
	if at < 0 {
		return 0, fmt.Errorf("token %q is negative: %w", token.Value, provider.ErrSyncTokenInvalid)
	}

	return at, nil
}

func (c *fakeConnector) CreateEvent(ctx context.Context, cred provider.Credential, calendarID string, event provider.Event) (provider.Event, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Event{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.write(cred, calendarID)
	if err != nil {
		return provider.Event{}, err
	}
	stored, err := c.accept(event)
	if err != nil {
		return provider.Event{}, err
	}

	c.nextEventID++
	stored.ID = "evt-" + strconv.Itoa(c.nextEventID)
	stored.ETag = "etag-" + strconv.Itoa(len(cal.log)+1)
	now := time.Now()
	stored.Created = now
	stored.Updated = now
	c.record(cal, stored)

	return stored, nil
}

func (c *fakeConnector) UpdateEvent(ctx context.Context, cred provider.Credential, calendarID string, event provider.Event) (provider.Event, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Event{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.write(cred, calendarID)
	if err != nil {
		return provider.Event{}, err
	}
	held, ok := cal.events[event.ID]
	if !ok {
		return provider.Event{}, fmt.Errorf("provider %q: event %q: %w", c.id, event.ID, provider.ErrUnknownEvent)
	}
	stored, err := c.accept(event)
	if err != nil {
		return provider.Event{}, err
	}

	stored.ETag = "etag-" + strconv.Itoa(len(cal.log)+1)
	stored.Created = held.Created
	stored.Updated = time.Now()
	c.record(cal, stored)

	return stored, nil
}

func (c *fakeConnector) DeleteEvent(ctx context.Context, cred provider.Credential, calendarID, eventID string) error {
	if err := c.begin(ctx, cred); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cal, err := c.write(cred, calendarID)
	if err != nil {
		return err
	}
	if _, held := cal.events[eventID]; !held {
		// Deleting what is already gone succeeds, so a run interrupted
		// after the provider acted is safe to repeat.
		return nil
	}

	delete(cal.events, eventID)
	cal.log = append(cal.log, provider.Change{Kind: provider.ChangeKindDelete, EventID: eventID})

	return nil
}

// begin runs the checks every method starts with: a live context and a
// credential that belongs to this connector.
func (c *fakeConnector) begin(ctx context.Context, cred provider.Credential) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("provider %q: %w", c.id, err)
	}

	return provider.CheckCredential(c.id, cred)
}

// read looks up a calendar the credential's connection reaches. A
// calendar on another connection is not distinguished from one that does
// not exist, because the credential cannot see it either way. The caller
// holds the lock.
func (c *fakeConnector) read(cred provider.Credential, id string) (*fakeCalendar, error) {
	cal, ok := c.calendars[id]
	if !ok || cal.connection != cred.Connection() {
		return nil, fmt.Errorf("provider %q: calendar %q: %w", c.id, id, provider.ErrUnknownCalendar)
	}

	return cal, nil
}

// write looks up a calendar for writing, and refuses one the credential
// may only read. The caller holds the lock.
func (c *fakeConnector) write(cred provider.Credential, id string) (*fakeCalendar, error) {
	cal, err := c.read(cred, id)
	if err != nil {
		return nil, err
	}
	if cal.meta.ReadOnly {
		return nil, fmt.Errorf("provider %q: calendar %q is read only: %w", c.id, id, provider.ErrUnsupported)
	}

	return cal, nil
}

// accept applies what the capabilities allow to an event on its way in:
// it refuses what the provider cannot express, and drops what it may
// drop. A real connector does exactly this before it builds a request.
func (c *fakeConnector) accept(event provider.Event) (provider.Event, error) {
	if err := event.Validate(); err != nil {
		return provider.Event{}, fmt.Errorf("provider %q: %w", c.id, err)
	}
	if event.Recurrence.IsMaster() && !c.caps.NativeRecurrence {
		return provider.Event{}, fmt.Errorf("provider %q: recurrence rule: %w", c.id, provider.ErrUnsupported)
	}
	if event.Start.Kind == provider.TimeKindFloating && !c.caps.FloatingTimes {
		return provider.Event{}, fmt.Errorf("provider %q: floating time: %w", c.id, provider.ErrUnsupported)
	}
	if !c.caps.Attendees {
		event.Attendees = nil
	}
	// Every provider names the organiser after the writing account, so a
	// connector drops what it was handed rather than pretending it stuck.
	event.Organizer = nil

	return event, nil
}

// record stores the event and appends the change. The caller holds the
// lock.
func (c *fakeConnector) record(cal *fakeCalendar, event provider.Event) {
	cal.events[event.ID] = event
	cal.log = append(cal.log, provider.Change{
		Kind:    provider.ChangeKindUpsert,
		EventID: event.ID,
		Event:   event,
	})
}
