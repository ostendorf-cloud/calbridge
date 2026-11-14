// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package providertest holds the in-memory connector every consumer of
// internal/provider tests against, and the worked example a real
// connector is written against.
//
// It exists because the contract has more than one consumer. The
// connector interface, the CalDAV connector and the sync engine all need
// a provider they can hold in memory, and a fake written again per
// package is a second reading of one contract. Two readings drift, and
// the one that drifts is the one a test then proves.
//
// The connector behaves the way internal/provider's documentation says a
// connector behaves:
//
//   - It refuses a credential meant for another provider, and one the
//     account would refuse.
//   - It refuses an operation its capabilities do not declare, and drops
//     what they allow it to drop.
//   - It pages a change read, and rejects a token it has invalidated.
//   - It rewrites content on write where it declares that it does.
//
// Nothing here makes a network call, so a test of the contract tests the
// contract and nothing else.
//
// # Telling a write from no write
//
// An identical rewrite leaves a calendar looking exactly as it did, so
// comparing the target's state proves nothing about whether a write
// happened. Counts reports the creates, updates and deletes the connector
// was actually asked for, which is the only place that difference is
// visible. Put and Edit change the stored events without going through a
// write, so a test's fixtures and a hand edit never move those counters.
package providertest

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

// ProviderID is the id the in-memory connector registers under. A real
// connector exports its own id from its own package, and this one names
// no real provider.
const ProviderID = provider.ID("memory")

// DefaultConnection is the connection a test uses where it has no reason
// to care which account a calendar belongs to.
const DefaultConnection = "conn-1"

// DefaultZone is the zone a calendar this connector creates carries, so a
// floating time written into it has somewhere to be anchored.
const DefaultZone = "Europe/Berlin"

// Token forms this connector hands out. A provider's token is opaque to
// everything above the connector, so the shape is this connector's
// business alone. Two forms are needed because the two positions a
// provider can be in are genuinely different: a place in the change log,
// which survives a restart, and a cursor into an initial snapshot, which
// does not.
const (
	logPrefix      = "log:"
	snapshotPrefix = "snapshot:"
)

// Credential is the shape a connector's own credential type takes: it
// names its provider and the connection it was loaded from, and carries
// no secret.
type Credential struct {
	connection string
}

// CredentialFor returns a credential bound to one connection. One
// connector serves every connection of its provider, and the credential
// is what picks the account.
func CredentialFor(connection string) Credential {
	return Credential{connection: connection}
}

// Provider returns the connector this credential authenticates against.
func (c Credential) Provider() provider.ID { return ProviderID }

// Connection returns the stored provider connection it was loaded from.
func (c Credential) Connection() string { return c.connection }

// calendar is one calendar the connector serves: the connection it
// belongs to, the events it holds now, and the log of every change made
// to it since it was created.
type calendar struct {
	connection string
	meta       provider.Calendar
	events     map[string]provider.Event
	log        []provider.Change
}

// snapshot returns the calendar's current events as upserts, ordered by
// id so a paged initial sync hands them over in a stable order.
func (c *calendar) snapshot() []provider.Change {
	ids := make([]string, 0, len(c.events))
	for id := range c.events {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]provider.Change, 0, len(ids))
	for _, id := range ids {
		out = append(out, provider.Change{
			Kind:    provider.ChangeKindUpsert,
			EventID: id,
			Event:   c.events[id],
		})
	}

	return out
}

// Connector is a calendar provider held in memory. Every method is safe
// for concurrent use, the way a real connector has to be.
type Connector struct {
	id provider.ID

	mu   sync.Mutex
	caps provider.Capabilities
	// pageSize is how many changes one read hands over. A larger run comes
	// back over several pages.
	pageSize int
	// incrementalState is what the connector reports while pages of an
	// incremental read remain. A provider whose mid-run token is a real
	// position reports PageStateResumable, one whose token only continues
	// the run reports PageStateContinued. A paged initial sync is always
	// PageStateContinued, because a snapshot cursor is worth nothing after
	// the run that opened it.
	incrementalState provider.PageState
	// normalise is the rewriting a write applies, for a connector
	// declaring Capabilities.NormalisesContent. The zero value changes
	// nothing.
	normalise func(provider.Event) provider.Event
	// rejected makes every call answer the way a provider answers a
	// credential the account revoked.
	rejected bool
	// writesRejectedAfter is how many writes still succeed before the
	// provider starts refusing the credential, counted from the moment it
	// is set. A negative value never refuses.
	writesRejectedAfter int

	calendars     map[string]*calendar
	invalidTokens map[string]bool
	writes        int
	nextEventID   int
	nextCalendar  int
	creates       int
	updates       int
	deletes       int
}

// Compile time proof that the fake is the thing it stands in for, and
// answers the two optional interfaces internal/provider defines.
var (
	_ provider.Connector         = (*Connector)(nil)
	_ provider.ContentNormaliser = (*Connector)(nil)
	_ provider.CredentialDecoder = (*Connector)(nil)
)

// New returns a connector declaring the given capabilities and holding no
// calendar yet. A test adds the calendars it needs with AddCalendar.
func New(caps provider.Capabilities) *Connector {
	return &Connector{
		id:                  ProviderID,
		caps:                caps,
		pageSize:            100,
		incrementalState:    provider.PageStateResumable,
		calendars:           map[string]*calendar{},
		invalidTokens:       map[string]bool{},
		writesRejectedAfter: -1,
	}
}

// AddCalendar puts a calendar on a connection and returns it with its id
// filled in.
func (c *Connector) AddCalendar(connection string, meta provider.Calendar) provider.Calendar {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.store(connection, meta)
}

// store puts a calendar into the connector. The caller holds the lock.
func (c *Connector) store(connection string, meta provider.Calendar) provider.Calendar {
	c.nextCalendar++
	meta.ID = "cal-" + strconv.Itoa(c.nextCalendar)
	c.calendars[meta.ID] = &calendar{
		connection: connection,
		meta:       meta,
		events:     map[string]provider.Event{},
	}

	return meta
}

// SetCapabilities changes what the provider declares, which is what a
// connector gaining or losing a capability between two passes looks like
// to a caller.
func (c *Connector) SetCapabilities(caps provider.Capabilities) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.caps = caps
}

// SetPageSize changes how many changes one read hands over.
func (c *Connector) SetPageSize(size int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pageSize = size
}

// SetIncrementalState changes what the connector reports while pages of
// an incremental read remain.
func (c *Connector) SetIncrementalState(state provider.PageState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.incrementalState = state
}

// SetNormalisation makes a write rewrite the event the way a provider
// that does not store what it was given rewrites it. It takes effect only
// where the declared capabilities report NormalisesContent, so a test
// that sets one and declares nothing gets a connector storing events as
// written, which is what the declaration promises.
func (c *Connector) SetNormalisation(rewrite func(provider.Event) provider.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.normalise = rewrite
}

// RejectCredential makes every later call answer with
// provider.ErrCredentialRejected, which is what an app specific password
// the user revoked and a refresh token the provider withdrew both look
// like.
func (c *Connector) RejectCredential(rejected bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.rejected = rejected
}

// RejectWritesAfter lets n more writes succeed and refuses the
// credential from the one after that, while reads keep working. That is
// a credential withdrawn partway through a pass: the pass read both
// calendars, and the account revoked the password before the writes were
// done.
func (c *Connector) RejectWritesAfter(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.writes = 0
	c.writesRejectedAfter = n
}

// writable reports whether the account still takes a write, and counts
// the attempt. The caller holds the lock.
func (c *Connector) writable() error {
	if c.writesRejectedAfter < 0 {
		return nil
	}
	c.writes++
	if c.writes <= c.writesRejectedAfter {
		return nil
	}

	return fmt.Errorf("provider %q refused a write: %w", c.id, provider.ErrCredentialRejected)
}

// Invalidate makes the connector reject the token from now on, which is
// how a provider that expired a position behaves.
func (c *Connector) Invalidate(token provider.SyncToken) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.invalidTokens[token.Value] = true
}

// Counts returns how many creates, updates and deletes the connector was
// asked for. It is the only way to tell an identical rewrite from no
// write at all, because both leave the calendar looking the same.
func (c *Connector) Counts() (creates, updates, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.creates, c.updates, c.deletes
}

// Events returns a calendar's events, ordered by id.
func (c *Connector) Events(calendarID string) []provider.Event {
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

// Holds reports whether a calendar still has an event.
func (c *Connector) Holds(calendarID, eventID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return false
	}
	_, held := cal.events[eventID]

	return held
}

// Put writes an event into a calendar directly, for the events a test
// needs in place before the caller under test ever runs. It counts as no
// write, so Counts still reports only what the caller asked for.
//
// An event carrying no id is given one, the same as a create would, so
// Put always adds. Changing an event already in the calendar is Replace,
// which refuses an event with no id: passing one to Put instead silently
// adds a second event, and a probe that counts nothing notices nothing.
func (c *Connector) Put(calendarID string, event provider.Event) provider.Event {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return provider.Event{}
	}
	if event.ID == "" {
		c.nextEventID++
		event.ID = "evt-" + strconv.Itoa(c.nextEventID)
	}
	c.record(cal, event)

	return event
}

// Replace overwrites an event already in a calendar, for a test that
// changes a fixture between two passes. It counts as no write, the same
// as Put.
//
// It refuses an event carrying no id, and one the calendar does not
// hold, so a test meaning to change a fixture cannot quietly add a second
// event instead. That mistake is invisible otherwise: the pass under test
// still runs, the probe still passes, and the calendar holds one event
// more than the test believes.
func (c *Connector) Replace(calendarID string, event provider.Event) (provider.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cal, ok := c.calendars[calendarID]
	if !ok {
		return provider.Event{}, fmt.Errorf("provider %q: calendar %q: %w", c.id, calendarID, provider.ErrUnknownCalendar)
	}
	// An empty id needs no check of its own: no event is stored under one,
	// so the lookup below is what refuses it.
	if _, held := cal.events[event.ID]; !held {
		return provider.Event{}, fmt.Errorf(
			"provider %q: calendar %q holds no event %q to replace: %w",
			c.id, calendarID, event.ID, provider.ErrUnknownEvent)
	}
	c.record(cal, event)

	return event, nil
}

// Edit changes an event the way a person changes it in their own calendar
// application: through the provider, behind the caller's back, and
// without moving the counters Counts reports.
func (c *Connector) Edit(calendarID, eventID string, change func(*provider.Event)) {
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
	c.record(cal, event)
}

// ID returns the provider id this connector is registered under.
func (c *Connector) ID() provider.ID { return c.id }

// Capabilities returns what the connector declares.
func (c *Connector) Capabilities() provider.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.caps
}

// NormaliseEvent implements provider.ContentNormaliser: it answers what a
// write turns an event into, without making one.
//
// It answers for everything a write changes, not only for the rewriting.
// A capability that drops a field drops it here too, because a caller
// comparing a copy against what it wrote meets the dropped field and the
// rewritten one the same way, and a normaliser that covers one and not
// the other is the bug this whole interface exists to kill.
func (c *Connector) NormaliseEvent(event provider.Event) (provider.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.rewrite(c.drop(event)), nil
}

// drop takes away what the capabilities say the provider does not store.
// The caller holds the lock.
func (c *Connector) drop(event provider.Event) provider.Event {
	if !c.caps.Attendees {
		event.Attendees = nil
	}
	// Every provider names the organiser after the writing account, so a
	// connector drops what it was handed rather than pretending it stuck.
	event.Organizer = nil

	return event
}

// rewrite applies the connector's normalisation. A connector that
// declares none stores what it was given, whatever a test set. The caller
// holds the lock.
func (c *Connector) rewrite(event provider.Event) provider.Event {
	if !c.caps.NormalisesContent || c.normalise == nil {
		return event
	}

	return c.normalise(event)
}

// namesAnInstance reports whether an event id is one an overridden
// occurrence carries. The ids this connector issues hold no separator, so
// one that does was derived from a master's.
func namesAnInstance(eventID string) bool {
	return strings.Contains(eventID, provider.InstanceSeparator)
}

// DecodeCredential implements provider.CredentialDecoder. The stored form
// is the connection's own id, because this connector has no secret to
// carry.
func (c *Connector) DecodeCredential(connection string, raw []byte) (provider.Credential, error) {
	if connection == "" {
		return nil, fmt.Errorf("provider %q: %w", c.id, provider.ErrCredentialMissing)
	}
	if stored := string(raw); stored != connection {
		return nil, fmt.Errorf(
			"provider %q: stored credential belongs to connection %q: %w",
			c.id, stored, provider.ErrCredentialMismatch)
	}

	return CredentialFor(connection), nil
}

// Calendars lists the calendars the credential reaches.
func (c *Connector) Calendars(ctx context.Context, cred provider.Credential) ([]provider.Calendar, error) {
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

// CreateCalendar creates a calendar calbridge owns.
func (c *Connector) CreateCalendar(ctx context.Context, cred provider.Credential, name string) (provider.Calendar, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Calendar{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.caps.CalendarCreation {
		return provider.Calendar{}, fmt.Errorf("provider %q: create calendar: %w", c.id, provider.ErrUnsupported)
	}

	return c.store(cred.Connection(), provider.Calendar{Name: name, TimeZone: DefaultZone, Managed: true}), nil
}

// DeleteCalendar removes a calendar calbridge created.
func (c *Connector) DeleteCalendar(ctx context.Context, cred provider.Credential, calendarID string) error {
	if err := c.begin(ctx, cred); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.caps.CalendarCreation {
		return fmt.Errorf("provider %q: delete calendar: %w", c.id, provider.ErrUnsupported)
	}

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

// Changes reads what changed in the calendar since token.
func (c *Connector) Changes(ctx context.Context, cred provider.Credential, calendarID string, token provider.SyncToken) (provider.ChangePage, error) {
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
func (c *Connector) page(cal *calendar, token provider.SyncToken) (provider.ChangePage, error) {
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
func (c *Connector) pending(cal *calendar, token provider.SyncToken) (changes []provider.Change, cursor func(int) string, state provider.PageState, err error) {
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

// CreateEvent writes an event into the calendar.
func (c *Connector) CreateEvent(ctx context.Context, cred provider.Credential, calendarID string, event provider.Event) (provider.Event, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Event{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.writable(); err != nil {
		return provider.Event{}, err
	}
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
	stored.Created = c.stamp()
	stored.Updated = stored.Created
	c.record(cal, stored)
	c.creates++

	return stored, nil
}

// UpdateEvent overwrites the event the id names.
func (c *Connector) UpdateEvent(ctx context.Context, cred provider.Credential, calendarID string, event provider.Event) (provider.Event, error) {
	if err := c.begin(ctx, cred); err != nil {
		return provider.Event{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.caps.InstanceWrites && namesAnInstance(event.ID) {
		return provider.Event{}, fmt.Errorf(
			"provider %q declares no instance writes and %q names one overridden occurrence: %w",
			c.id, event.ID, provider.ErrUnsupported)
	}
	if err := c.writable(); err != nil {
		return provider.Event{}, err
	}
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
	stored.Updated = c.stamp()
	c.record(cal, stored)
	c.updates++

	return stored, nil
}

// DeleteEvent removes an event from the calendar.
func (c *Connector) DeleteEvent(ctx context.Context, cred provider.Credential, calendarID, eventID string) error {
	if err := c.begin(ctx, cred); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.caps.InstanceWrites && namesAnInstance(eventID) {
		// Deleting the master's id removes the occurrence with it, which
		// is what the interface documents a connector declaring no
		// instance writes does.
		return fmt.Errorf(
			"provider %q declares no instance writes and %q names one overridden occurrence: %w",
			c.id, eventID, provider.ErrUnsupported)
	}
	if err := c.writable(); err != nil {
		return err
	}
	cal, err := c.write(cred, calendarID)
	if err != nil {
		return err
	}
	if _, held := cal.events[eventID]; !held {
		// Deleting what is already gone succeeds, so a run interrupted
		// after the provider acted is safe to repeat.
		return nil
	}

	// A provider that declares no instance writes keeps its overridden
	// occurrences inside the master's own object, so removing the master
	// removes them with it and there is one deletion to report. That is
	// what provider.ChangeKindDelete documents, and a fake that left the
	// overrides standing would let a caller pass a test the real provider
	// fails.
	for _, id := range c.removedWith(cal, eventID) {
		delete(cal.events, id)
		cal.log = append(cal.log, provider.Change{Kind: provider.ChangeKindDelete, EventID: id})
	}
	c.deletes++

	return nil
}

// removedWith returns every event id one deletion takes, which is the id
// itself and, for a provider that stores its overrides inside the
// master's object, the occurrences derived from it. The caller holds the
// lock.
func (c *Connector) removedWith(cal *calendar, eventID string) []string {
	out := []string{eventID}
	if c.caps.InstanceWrites {
		return out
	}

	for id := range cal.events {
		if provider.IsInstanceIDOf(id, eventID) {
			out = append(out, id)
		}
	}
	slices.Sort(out)

	return out
}

// stamp is the moment a write records. It advances by a minute per write,
// so two writes in one test are ordered without a wall clock reading
// reaching a test's expectations. The caller holds the lock.
func (c *Connector) stamp() time.Time {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	return base.Add(time.Duration(c.creates+c.updates+c.deletes) * time.Minute)
}

// begin runs the checks every method starts with: a live context, a
// credential that belongs to this connector, and an account that has not
// withdrawn it.
func (c *Connector) begin(ctx context.Context, cred provider.Credential) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("provider %q: %w", c.id, err)
	}
	if err := provider.CheckCredential(c.id, cred); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rejected {
		return fmt.Errorf("provider %q refused connection %q: %w", c.id, cred.Connection(), provider.ErrCredentialRejected)
	}

	return nil
}

// read looks up a calendar the credential's connection reaches. A
// calendar on another connection is not distinguished from one that does
// not exist, because the credential cannot see it either way. The caller
// holds the lock.
func (c *Connector) read(cred provider.Credential, id string) (*calendar, error) {
	cal, ok := c.calendars[id]
	if !ok || cal.connection != cred.Connection() {
		return nil, fmt.Errorf("provider %q: calendar %q: %w", c.id, id, provider.ErrUnknownCalendar)
	}

	return cal, nil
}

// write looks up a calendar for writing, and refuses one the credential
// may only read. The caller holds the lock.
func (c *Connector) write(cred provider.Credential, id string) (*calendar, error) {
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
// it refuses what the provider cannot express, drops what it may drop,
// and rewrites what it declares it rewrites. A real connector does
// exactly this before it builds a request. The caller holds the lock.
func (c *Connector) accept(event provider.Event) (provider.Event, error) {
	if err := event.Validate(); err != nil {
		return provider.Event{}, fmt.Errorf("provider %q: %w", c.id, err)
	}
	if event.Recurrence.IsMaster() && !c.caps.NativeRecurrence {
		return provider.Event{}, fmt.Errorf("provider %q: recurrence rule: %w", c.id, provider.ErrUnsupported)
	}
	if event.Recurrence.IsInstance() && !c.caps.InstanceWrites {
		return provider.Event{}, fmt.Errorf("provider %q: overridden occurrence: %w", c.id, provider.ErrUnsupported)
	}
	if event.Start.Kind == provider.TimeKindFloating && !c.caps.FloatingTimes {
		return provider.Event{}, fmt.Errorf("provider %q: floating time: %w", c.id, provider.ErrUnsupported)
	}

	return c.rewrite(c.drop(event)), nil
}

// record stores the event and appends the change. The caller holds the
// lock.
func (c *Connector) record(cal *calendar, event provider.Event) {
	cal.events[event.ID] = event
	cal.log = append(cal.log, provider.Change{
		Kind:    provider.ChangeKindUpsert,
		EventID: event.ID,
		Event:   event,
	})
}
