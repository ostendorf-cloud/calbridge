// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package provider defines the one interface every calendar provider
// implements, the registry connectors are resolved from, the capabilities
// each connector declares, and the normalised event every connector
// speaks.
//
// A connector translates one provider into this package's types and
// nothing else leaves it. Provider ids, timezone catalogues, payload
// shapes and quirks stay inside the connector's own package, so no caller
// ever switches on a provider name: it resolves a Connector from a
// Registry by ID and reads Capabilities to pick a strategy.
//
// # Normalisation rules
//
// These rules are the contract a connector obeys. They are what keeps one
// provider's model from reaching the sync engine.
//
//  1. Times. Every endpoint is an EventTime of exactly one kind: a
//     calendar date for an all-day event, a wall time in a named zone, or
//     a floating wall time that belongs to no zone. A connector maps its
//     provider's representation onto one of the three and never invents a
//     fourth.
//  2. Zones. A zoned time names an IANA zone, such as "Europe/Berlin". A
//     connector whose provider keeps another zone catalogue translates in
//     both directions inside its own package. An absolute instant is a
//     zoned time in "UTC", not a kind of its own.
//  3. Floating times. A connector returns a floating time only where the
//     provider really stores one. Writing a floating time into a provider
//     that always attaches a zone is the caller's decision, taken from
//     Capabilities.FloatingTimes, not a silent substitution by the
//     connector.
//  4. All-day. An all-day endpoint carries a date and nothing else: the
//     time of day is zero and the zone is empty.
//  5. End is exclusive. Event.End is the first moment the event no longer
//     occupies. For an all-day event that is the day after its last day.
//  6. Start and End carry the same TimeKind. Their zones may differ, for
//     an event that starts in one zone and ends in another.
//  7. Identity. Event.ID is the provider's own id for the event in that
//     calendar and stays opaque outside the connector. Event.UID is the
//     RFC 5545 UID where the provider exposes one and is empty otherwise.
//  8. Recurrence. A recurring master carries Recurrence.Rule. One
//     occurrence carries Recurrence.MasterID and Recurrence.OriginalStart
//     and carries no rule. A connector that cannot express its provider's
//     rules as RFC 5545 lines reports NativeRecurrence as false rather
//     than approximating them.
//  9. Deletion. A change feed reports a removed event as a Change of kind
//     ChangeKindDelete. A connector never reports it as an event whose
//     status is StatusCancelled, so the caller has one thing to look at.
//  10. Defaults. A content enum, such as Status or Visibility, has a
//     useful zero value, because an omitted one is harmless. A structural
//     enum, such as TimeKind or PageState, has an invalid zero value,
//     because an omitted one would silently lose data.
//
// # Reading changes
//
// Connector.Changes is the one way a change is noticed, whether the
// provider pushes a notification or the engine polls on the reconcile
// interval. One call returns one ChangePage, and three answers are
// possible.
//
//  1. The provider handed over everything: PageStateComplete, and the
//     page's token is the position stored for the next run.
//  2. The provider has more: PageStateResumable or PageStateContinued,
//     and the caller calls again with the page's token. The two differ in
//     whether that token is also a position worth storing, which is what
//     decides the cost of a run cut short. A response the provider
//     truncated under its own limit is a page like any other, because the
//     token that comes with it covers exactly what was delivered.
//  3. The provider rejected the token: ErrSyncTokenInvalid, and the
//     caller drops its position and calls again with the zero SyncToken,
//     which starts an initial sync.
//
// # Registration
//
// A connector does not register itself from an init function: the
// registry is a value built in cmd/calbridge's wiring, so there is no
// package level mutable state and a test builds its own. Adding a
// provider stays a new package and one Register call in that wiring, and
// never an edit to a switch statement in the engine.
package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// ID identifies one provider. A connector package owns its own id as an
// exported constant, so this package names no provider.
type ID string

// idPattern is the shape an ID must have: lowercase ASCII letters,
// digits and hyphens, so an id is safe in a URL path, a database key and
// a metric label without escaping.
var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// maxIDLength bounds an ID, so a provider id always fits the columns and
// labels that carry it.
const maxIDLength = 32

// Validate reports whether the id has the documented shape.
func (id ID) Validate() error {
	switch {
	case id == "":
		return fmt.Errorf("provider id %q: %w", string(id), ErrInvalidID)
	case len(id) > maxIDLength:
		return fmt.Errorf("provider id %q is longer than %d characters: %w", string(id), maxIDLength, ErrInvalidID)
	case !idPattern.MatchString(string(id)):
		return fmt.Errorf("provider id %q is not lowercase letters, digits and hyphens: %w", string(id), ErrInvalidID)
	default:
		return nil
	}
}

// String returns the id as written.
func (id ID) String() string { return string(id) }

// Errors every connector and the registry share. A caller compares them
// with errors.Is and never on the message.
var (
	// ErrInvalidID reports a provider id that does not have the shape
	// ID.Validate describes.
	ErrInvalidID = errors.New("invalid provider id")
	// ErrUnknownProvider reports that no connector is registered under
	// the requested id.
	ErrUnknownProvider = errors.New("unknown provider")
	// ErrDuplicateProvider reports a second connector registering under
	// an id that is already taken.
	ErrDuplicateProvider = errors.New("duplicate provider")
	// ErrNilConnector reports a registration with no connector behind it.
	ErrNilConnector = errors.New("connector is nil")
	// ErrUnsupported reports an operation the connector's Capabilities
	// already declared it does not have. A caller that reads the
	// capability first never sees it.
	ErrUnsupported = errors.New("operation not supported by this provider")
	// ErrSyncTokenInvalid reports that the provider rejected the sync
	// token. The caller discards its stored position and calls Changes
	// again with the zero SyncToken, which starts an initial sync.
	ErrSyncTokenInvalid = errors.New("sync token invalid")
	// ErrCredentialMissing reports a call made without a credential.
	ErrCredentialMissing = errors.New("credential missing")
	// ErrCredentialMismatch reports a credential handed to a connector it
	// does not belong to.
	ErrCredentialMismatch = errors.New("credential belongs to another provider")
	// ErrUnknownCalendar reports a calendar id the credential does not
	// reach, which is what a calendar removed or unshared since the last
	// run looks like.
	ErrUnknownCalendar = errors.New("unknown calendar")
	// ErrUnknownEvent reports an event id the calendar does not hold.
	ErrUnknownEvent = errors.New("unknown event")
	// ErrInvalidEvent reports an event that breaks the normalisation
	// rules in this package's documentation.
	ErrInvalidEvent = errors.New("invalid event")
	// ErrInvalidChange reports a change or a page of changes a caller
	// cannot act on.
	ErrInvalidChange = errors.New("invalid change")
)

// Credential is the handle a connector authenticates with. Each connector
// defines its own concrete type and asserts it back, so this package
// never sees a secret: loading, decrypting and refreshing the material
// behind the handle belong to the storage and auth layers.
type Credential interface {
	// Provider names the connector this credential authenticates against.
	Provider() ID
	// Connection names the stored provider connection it was loaded from.
	// A connector uses it to key whatever per-connection state it caches,
	// and a caller uses it to correlate a log line with a connection.
	Connection() string
}

// CheckCredential reports whether cred may be used with the connector
// registered under id. Every connector calls it before its first request,
// so a credential routed to the wrong provider fails before it reaches
// the network.
func CheckCredential(id ID, cred Credential) error {
	if cred == nil {
		return fmt.Errorf("provider %q: %w", id, ErrCredentialMissing)
	}
	if got := cred.Provider(); got != id {
		return fmt.Errorf("provider %q got a credential for %q: %w", id, got, ErrCredentialMismatch)
	}

	return nil
}

// Connector is one provider, and the only surface the sync engine has on
// it. Every method is safe for concurrent use: one connector serves every
// connection of its provider, and the credential selects the account.
type Connector interface {
	// ID returns the provider id this connector is registered under. It
	// is constant for the connector's lifetime.
	ID() ID

	// Capabilities returns what the provider can do. It is constant for
	// the connector's lifetime, so a caller may read it once at startup
	// and a gap is known at registration rather than at runtime.
	Capabilities() Capabilities

	// Calendars lists the calendars the credential reaches, including the
	// ones it may only read.
	Calendars(ctx context.Context, cred Credential) ([]Calendar, error)

	// CreateCalendar creates a calendar calbridge owns and returns it. A
	// connector whose Capabilities report CalendarCreation as false
	// returns ErrUnsupported and creates nothing.
	CreateCalendar(ctx context.Context, cred Credential, name string) (Calendar, error)

	// Changes reads what changed in the calendar since token, and returns
	// one page. The zero SyncToken asks for an initial sync, which
	// reports every event in the calendar and no deletion. A token the
	// provider rejects returns ErrSyncTokenInvalid, and the caller
	// restarts from an initial sync. ChangePage.State says whether to
	// call again and whether the returned token may be stored.
	Changes(ctx context.Context, cred Credential, calendarID string, token SyncToken) (ChangePage, error)

	// CreateEvent writes event into the calendar and returns it as the
	// provider stored it, with Event.ID and Event.ETag filled in. The
	// event's Marker survives the round trip and comes back on every
	// later read, which is what lets the engine recognise its own writes
	// and refuse to copy them onward.
	CreateEvent(ctx context.Context, cred Credential, calendarID string, event Event) (Event, error)

	// UpdateEvent overwrites the event event.ID names with event, and
	// returns it as the provider stored it. An id the calendar does not
	// hold returns ErrUnknownEvent.
	UpdateEvent(ctx context.Context, cred Credential, calendarID string, event Event) (Event, error)

	// DeleteEvent removes the event from the calendar. Deleting an event
	// that is already gone succeeds, so an interrupted run is safe to
	// repeat.
	DeleteEvent(ctx context.Context, cred Credential, calendarID, eventID string) error
}
