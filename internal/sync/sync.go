// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package sync turns a source change into target operations by applying
// an edge's field mask. It is the only package that knows what a detail
// level means: everywhere else a detail level is a string that comes from
// the user and goes into the database.
//
// # What an edge does
//
// An edge copies events from one source calendar into one target
// calendar, in one direction, at one detail level. The detail level is a
// preset, and a preset is a FieldMask the user may override field by
// field. Start and end are never masked: an event the target cannot
// place in time is not a copy of anything.
//
// The engine writes into whichever calendar the edge names, whether
// calbridge created it or the user picked an existing one. The difference
// shows at teardown: a calendar calbridge created is removed with the
// edge, and a calendar the user picked keeps existing with its own events
// untouched.
//
// # Loop prevention
//
// Every event the engine writes carries a provider.Marker naming the edge
// that wrote it and the source event it was made from. An event arriving
// as a source change with a marker on it is calbridge's own write, and is
// never copied onward: the operation records ReasonOwnWrite and nothing
// reaches the target. That is what stops a mirror from feeding itself.
//
// The marker breaks a running loop. CheckLoop refuses to build one:
// adding an edge whose target already reaches its source, the opposing
// edge included, fails with ErrLoop before the first event moves.
//
// # Idempotence
//
// The mapping table decides whether a write happens at all. Each copy
// carries the hash of the content the engine projected, and a projection
// hashing to the stored value is not written again. An identical rewrite
// would leave the target looking identical, so the saving is only visible
// as a call the connector never received.
//
// # Recurrence
//
// A repeating source event is copied as one master carrying its RFC 5545
// rule where the target's declared capabilities take one and the mask
// copies recurrence. Where they do not, the engine expands the rule into
// concrete instances inside a rolling Window and copies those, one event
// per occurrence. The decision reads Capabilities, never a provider name.
//
// Two limits of the expansion fallback are worth knowing. A rule the
// expander does not implement is reported as ErrUnsupportedRecurrence
// rather than approximated. An occurrence the source overrides with its
// own instance keeps the instance's time and content only on a full
// reconcile, where the engine sees the whole source calendar at once; a
// single instance change on its own is covered by its master and skipped.
//
// The two strategies key their copies differently, the master's own id
// against one id per occurrence, so a copy can end up under a key the
// edge no longer writes to: the detail level changed, the target's
// capabilities changed, or the source event stopped repeating. A full
// pass decides a mapping row's fate by asking whether this edge still
// writes to its key, which is one question covering all three, rather
// than by checking the routes that lead there. Removals are reported as
// ReasonStrategyChanged. Without it, lowering an edge's detail level
// would leave the wider copy standing for good.
//
// # Reconcile
//
// Reconcile is a full pass over one edge: it reads the source calendar
// from the zero sync token, which is an initial sync, so it depends on no
// stored position and on no push notification having been delivered. It
// creates what is missing, overwrites what drifted, and deletes both the
// copies whose source is gone and the marked events in the target that no
// mapping row claims.
//
// # An event edited by hand in the target
//
// The engine overwrites it on the next reconcile. A copy is a projection
// of its source, not a document of its own, and an edit that survives
// would quietly stop matching the calendar it claims to mirror. The
// reasoning is recorded in docs/adr/0001-overwrite-hand-edited-mirrored-events.md.
//
// One edit it does not overwrite is a single occurrence of a mirrored
// repeating event, where the target provider keeps the override inside
// the master's own object. There is no id such a provider accepts for it,
// so the engine never asks: it reports the occurrence as
// ReasonCoveredByMaster and leaves it standing.
//
// What removes it is the master's next write, which replaces the whole
// object. Only a changed source leads to that write, so an override on a
// master whose source never changes again stands indefinitely, reported
// as covered on every pass. Deleting the edge removes it, because that
// removes the master's object.
//
// Where no master of it is in the calendar at all, nothing the engine can
// name removes it, and the pass reports ReasonInstanceUnreachable rather
// than looking like it swept a calendar it did not. That one is permanent
// for the life of the edge, and it is the one exception to the teardown
// promise below.
//
// # Teardown
//
// Deleting an edge deletes every event it wrote, every mapping row it
// held, and the target calendar itself where calbridge created it. A
// grant that no longer covers an edge has the same effect, the edge row
// included: the engine checks coverage on every pass, because the
// database trigger behind edges holds that invariant when an edge is
// written and not afterwards. The reasoning, and what it costs the
// grantee, is recorded in
// docs/adr/0002-a-revoked-grant-deletes-the-edge.md.
package sync

import "errors"

// Errors this package returns. A caller compares them with errors.Is and
// never on the message.
var (
	// ErrInvalidDetailLevel reports a detail level that is not one of the
	// presets.
	ErrInvalidDetailLevel = errors.New("invalid detail level")
	// ErrInvalidBinding reports an edge resolved to something the engine
	// cannot act on, such as an endpoint with no connector.
	ErrInvalidBinding = errors.New("invalid binding")
	// ErrIncompleteOptions reports an engine built without a dependency it
	// needs.
	ErrIncompleteOptions = errors.New("incomplete engine options")
	// ErrLoop reports an edge whose target already reaches its source, so
	// the two would copy each other forever.
	ErrLoop = errors.New("edge closes a sync loop")
	// ErrGrantMissing reports an edge over a calendar its own user neither
	// owns nor holds a grant for. The database refuses such an edge when
	// it is written; this is the same invariant checked again on every
	// pass, which is what catches a grant revoked after the fact.
	ErrGrantMissing = errors.New("no grant covers this edge")
	// ErrGrantExceeded reports an edge whose detail level copies more than
	// the grant over its calendar allows.
	ErrGrantExceeded = errors.New("edge exceeds the grant's detail level")
	// ErrNoTargetZone reports a floating source time written into a target
	// that stores no floating time and whose calendar names no zone, so
	// there is nothing to anchor it in.
	ErrNoTargetZone = errors.New("target calendar names no time zone")
	// ErrUnsupportedRecurrence reports a recurrence rule the expander does
	// not implement. The engine reports it rather than copying an
	// approximation of the series.
	ErrUnsupportedRecurrence = errors.New("unsupported recurrence rule")
	// ErrRecurrenceTooLarge reports a rule producing more occurrences
	// inside the window than one edge may write.
	ErrRecurrenceTooLarge = errors.New("recurrence expands beyond the occurrence limit")
	// ErrTooManyPages reports a calendar whose change feed did not finish
	// inside the page limit, which is a connector handing out pages
	// forever rather than a calendar that large.
	ErrTooManyPages = errors.New("change feed exceeded the page limit")
)
