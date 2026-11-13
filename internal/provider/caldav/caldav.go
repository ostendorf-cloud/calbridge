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
// above this package. A URL is only followed when its host matches the
// profile's HostSuffixes, so a server's own answer cannot aim the
// account's credential somewhere else.
//
// # Recurrence
//
// One CalDAV resource holds a recurring master and every overridden
// occurrence of it, as several VEVENTs sharing a UID. The connector
// reports the master under the resource name and each override under the
// resource name, a "#", and its RECURRENCE-ID, so the sync engine sees
// the normalised one event per entry it expects.
//
// Writing one overridden occurrence on its own is not available: it
// would be a read, a change inside the resource and a write back, and
// provider.Capabilities has no field that would let a caller know in
// advance. Such a write returns provider.ErrUnsupported.
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
	ErrAuthentication = errors.New("caldav server refused the credential")
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
		client:   &http.Client{Timeout: defaultTimeout},
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
func (c *Connector) Capabilities() provider.Capabilities {
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
	if len(c.profiles) == 0 {
		return provider.Capabilities{}
	}

	return caps
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
