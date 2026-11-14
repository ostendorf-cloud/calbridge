// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package caldav is the generic CalDAV connector, driven by a Profile
// per server. iCloud is the first profile, and the type exists so a
// second server is an entry in Profiles rather than a branch in a
// request.
//
// # What it speaks
//
// The connector reads and writes plain WebDAV and CalDAV over HTTPS,
// with no dependency beyond the standard library and the UUID generator
// the module already uses:
//
//   - Discovery walks DAV:current-user-principal (RFC 5397),
//     CALDAV:calendar-home-set (RFC 4791 section 6.2.1) and a PROPFIND of
//     the home's members (RFC 4918 section 9.1).
//   - Reading changes is the DAV:sync-collection report of RFC 6578,
//     followed by the CALDAV:calendar-multiget report of RFC 4791 section
//     7.9 for the resources it named.
//   - Writing is a PUT, and removing is a DELETE, of one iCalendar object
//     resource (RFC 4791 section 5.3.2), with MKCALENDAR (section 5.3.1)
//     for a calendar calbridge owns.
//
// # Identities
//
// provider.Calendar.ID is the collection's absolute URL, and an event id
// is the resource's own name inside that collection. Both stay opaque
// above this package. A URL is only followed when its scheme is https
// and its host matches the profile's HostSuffixes, so a server's own
// answer cannot aim the account's credential somewhere else. That holds
// for an href in a response body and for the Location of a redirect
// alike: the HTTP client follows no redirect of its own, and every hop
// goes through the same check as the first request.
//
// # Text from a source calendar
//
// Every value written into an object goes through icalWriter.line,
// which refuses a line break rather than writing it. A break in a value
// would end the content line and begin another, so a UID or a
// recurrence rule carrying one would add a property this package never
// wrote, an ATTENDEE among them. See ical.go for why the refusal is the
// choice rather than stripping the break.
//
// # Recurrence
//
// One CalDAV resource holds a recurring master and every overridden
// occurrence of it, as several VEVENTs sharing a UID. The connector
// reports the master under the resource name and each override under an
// id provider.InstanceIDOf derives from it, so the sync engine sees the
// normalised one event per entry it expects.
//
// The connector declares provider.Capabilities.InstanceWrites as false,
// which is the interface's own way of saying that one occurrence is not
// written or deleted on its own here. A caller reading that capability
// changes the master instead, and never meets provider.ErrUnsupported.
// One deletion of the resource is one deletion of the master's id, and
// internal/provider's ninth normalisation rule is what makes the
// overrides go with it.
//
// # Normalisation on write
//
// The connector declares provider.Capabilities.NormalisesContent and
// implements provider.ContentNormaliser. An iCalendar object is not a
// transparent carrier of every string, so an event does not always read
// back as it was written. NormaliseEvent says what a write turns an
// event into, by running the connector's own encode and decode over it.
//
// Without that declaration a caller comparing a copy against what it
// wrote reads the rewriting as somebody's hand edit, and rewrites the
// event on every pass for as long as the edge exists.
//
// # Time zones
//
// RFC 5545 section 3.6.5 requires an iCalendar object to carry a
// VTIMEZONE for every TZID it names, so the connector builds one from
// the Go time zone database for every zoned event it writes. See
// vtimezone.go for what that covers.
package caldav

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// ID is the provider id this connector registers under. Every CalDAV
// server shares it, because the profile, not the id, is what separates
// two servers.
const ID = provider.ID("caldav")

// Errors this connector adds to the ones internal/provider defines. A
// caller compares them with errors.Is.
var (
	// ErrUnknownProfile reports a credential naming a CalDAV server this
	// connector has no profile for.
	ErrUnknownProfile = errors.New("unknown caldav profile")
	// ErrInvalidProfile reports a profile that cannot drive a request.
	ErrInvalidProfile = errors.New("invalid caldav profile")
	// ErrMalformedCredential reports stored credential bytes this
	// connector cannot read back.
	ErrMalformedCredential = errors.New("malformed caldav credential")
	// ErrAuthentication reports that the server refused the credential,
	// which is what a revoked or mistyped app specific password looks
	// like. It is the answer to a 401 and to a 403 the server gave on the
	// first reachable request, so a revoked password surfaces at once
	// instead of as an empty calendar list.
	//
	// It wraps provider.ErrCredentialRejected, so the engine stops the
	// connection's passes without knowing this connector exists, and a
	// message that names CalDAV still reaches a log line.
	ErrAuthentication = fmt.Errorf("caldav server refused the credential: %w", provider.ErrCredentialRejected)
	// ErrServer reports a response this connector cannot act on: a status
	// outside the ones the specifications define for the request, or a
	// body it cannot parse.
	ErrServer = errors.New("caldav server response")
	// ErrForbiddenHost reports a URL the server handed over that points
	// outside the profile's HostSuffixes, which the connector refuses to
	// send the account's credential to.
	ErrForbiddenHost = errors.New("caldav url outside the profile's hosts")
	// ErrConflict reports a write the server refused because the resource
	// changed underneath, or because another resource already holds the
	// event's UID.
	ErrConflict = errors.New("caldav write conflicted")
)

// defaultTimeout bounds one request. A CalDAV server answering a sync
// report over a large calendar is slow, and a connection that stalls is
// what the reconcile loop must not inherit.
const defaultTimeout = 60 * time.Second

// Doer is the HTTP surface the connector needs, defined here because
// this package is the consumer. A test supplies its own.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Connector is the CalDAV connector. One instance serves every CalDAV
// connection of every profile: the credential names the profile and the
// account. It holds no per connection state, so it is safe for
// concurrent use.
type Connector struct {
	client Doer
	// profiles is the set of servers this connector serves, taken from
	// Profiles at construction so a test builds its own.
	profiles map[string]Profile
	// now is the clock, injected so a written event's DTSTAMP is
	// deterministic in a test.
	now func() time.Time
	// newUID generates the UID of an event this connector creates,
	// injected for the same reason.
	newUID func() string
}

// Option adjusts a Connector at construction.
type Option func(*Connector)

// WithClient replaces the HTTP client.
func WithClient(d Doer) Option {
	return func(c *Connector) {
		if d != nil {
			c.client = d
		}
	}
}

// WithProfiles replaces the set of servers the connector serves.
func WithProfiles(profiles map[string]Profile) Option {
	return func(c *Connector) {
		if profiles != nil {
			c.profiles = profiles
		}
	}
}

// WithClock replaces the clock the connector stamps a write with.
func WithClock(now func() time.Time) Option {
	return func(c *Connector) {
		if now != nil {
			c.now = now
		}
	}
}

// WithUIDs replaces the generator of the UID a created event carries.
func WithUIDs(newUID func() string) Option {
	return func(c *Connector) {
		if newUID != nil {
			c.newUID = newUID
		}
	}
}

// New returns a connector over the shipped profiles.
func New(opts ...Option) *Connector {
	c := &Connector{
		client: &http.Client{
			Timeout: defaultTimeout,
			// A redirect is never followed by the HTTP client itself. The
			// client knows no profile, so it cannot tell whether the
			// target is a host this credential may reach, and net/http
			// decides whether to carry the Authorization header onward by
			// comparing hosts alone: a same-host redirect from https to
			// http keeps the header and puts the app specific password on
			// the wire in clear. do follows a redirect itself instead, and
			// puts every hop through the same scheme and host check as the
			// first request.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		profiles: Profiles,
		now:      time.Now,
		newUID:   newUID,
	}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// ID returns the provider id.
func (c *Connector) ID() provider.ID { return ID }

// Capabilities returns what a CalDAV server can do, as the shipped
// profiles agree on it.
//
// The interface asks one connector for one answer, and this connector
// serves several servers. Where two profiles disagree, the answer is the
// one every profile supports, because a caller that reads a capability
// and finds it true must be able to act on it against any connection of
// this provider. With iCloud as the only profile, the answer is iCloud's
// own.
// NormalisesContent is the one capability that is not an intersection.
// It says the connector rewrites content on write, and this connector
// does so in its own iCalendar mapping rather than on any server's
// behalf, so it holds for every profile. Declaring it false where one
// profile happened to leave it unset would be the unsafe answer: a caller
// would compare what it wrote against what it reads back, find the
// rewriting, and take it for somebody's hand edit.
func (c *Connector) Capabilities() provider.Capabilities {
	if len(c.profiles) == 0 {
		return provider.Capabilities{}
	}

	caps := provider.Capabilities{
		NativeRecurrence: true,
		CalendarCreation: true,
		Attendees:        true,
		FloatingTimes:    true,
	}
	for _, p := range c.profiles {
		caps.NativeRecurrence = caps.NativeRecurrence && p.Capabilities.NativeRecurrence
		caps.CalendarCreation = caps.CalendarCreation && p.Capabilities.CalendarCreation
		caps.Attendees = caps.Attendees && p.Capabilities.Attendees
		caps.FloatingTimes = caps.FloatingTimes && p.Capabilities.FloatingTimes
	}
	// InstanceWrites stays false and is read from no profile. A CalDAV
	// resource holds a master and every override of it under one UID, so
	// one occurrence has no resource of its own to write or delete. That
	// is the format rather than a server's choice, and Profile.Validate
	// refuses a profile claiming otherwise.
	caps.NormalisesContent = true

	return caps
}

// NormaliseEvent returns the event as a write of it and a read back
// would give it, which is not the event it was handed.
//
// This connector stores an event as an iCalendar object and reads the
// object back, and RFC 5545 is not a transparent carrier of every string.
// Section 3.3.11 gives a text value one escape for a newline, so a
// description holding a carriage return comes back holding a line feed.
// Section 3.1's parameter form carries no quotation mark inside a quoted
// value, so an attendee's display name loses one. A TZID the zone
// database does not know is read back as a floating time, because the
// second normalisation rule of internal/provider allows no other answer.
//
// A caller comparing a copy against what it wrote needs this. Without it
// every difference above reads as somebody editing the copy by hand, and
// the copy is rewritten on every pass for as long as the edge exists.
//
// The answer is the write path itself, not a description of it: the same
// accept, the same encode and the same decode, so the declaration cannot
// drift away from what a write does. It therefore refuses what a write
// refuses, and drops what the declared capabilities drop.
//
// What it does not model is the server. It answers for this connector's
// mapping between provider.Event and an iCalendar object, and a server
// that rewrites a value of its own, in any field the caller hashes, is
// still a difference the caller reads as a hand edit. No CalDAV server
// calbridge targets is known to do that, and the measurement that would
// settle it belongs to the profile rather than here.
//
// The fields the server owns are restored afterwards: the object carries
// no resource name and no entity tag, and a write is what fills those
// in.
func (c *Connector) NormaliseEvent(event provider.Event) (provider.Event, error) {
	caps := c.Capabilities()
	accepted, err := accept(caps, event)
	if err != nil {
		return provider.Event{}, err
	}

	object, err := encodeResource(accepted, c.now().UTC(), caps)
	if err != nil {
		return provider.Event{}, err
	}

	name := event.ID
	if name == "" {
		// decodeEvent names every event it returns after the resource it
		// came from, and an event on its way to a create has no id yet.
		// Any name does here, because the result's is overwritten below.
		name = "unwritten" + resourceSuffix
	}
	decoded, err := decodeResource(name, object)
	if err != nil {
		return provider.Event{}, err
	}

	out := decoded[0]
	out.ID = event.ID
	out.UID = event.UID
	out.ETag = event.ETag
	out.Created = event.Created
	out.Updated = event.Updated

	return out, nil
}

// profileFor returns the profile the credential names, after checking
// the credential belongs to this connector at all.
func (c *Connector) profileFor(cred provider.Credential) (Profile, Credential, error) {
	if err := provider.CheckCredential(ID, cred); err != nil {
		return Profile{}, Credential{}, err
	}
	own, ok := cred.(Credential)
	if !ok {
		return Profile{}, Credential{}, fmt.Errorf("caldav got a %T: %w", cred, provider.ErrCredentialMismatch)
	}
	p, ok := c.profiles[own.profile]
	if !ok {
		return Profile{}, Credential{}, fmt.Errorf("caldav profile %q: %w", own.profile, ErrUnknownProfile)
	}
	if err := p.Validate(); err != nil {
		return Profile{}, Credential{}, err
	}

	return p, own, nil
}

// basicEncode returns the credentials half of an RFC 7617 Basic
// credential.
func basicEncode(account, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(account + ":" + password))
}
