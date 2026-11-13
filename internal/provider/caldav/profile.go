// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"fmt"
	"strings"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// AuthStyle is how a profile's server authenticates a request.
type AuthStyle uint8

const (
	// AuthStyleUnset is the zero value and never valid, so a profile that
	// names no authentication is caught when it is looked up rather than
	// at the first request.
	AuthStyleUnset AuthStyle = iota
	// AuthStyleBasic sends the account name and the password in an HTTP
	// Basic credential, over TLS. It is what iCloud takes, because Apple
	// publishes no OAuth flow for calendar access.
	AuthStyleBasic
)

// String returns the style's name.
func (a AuthStyle) String() string {
	switch a {
	case AuthStyleUnset:
		return "unset"
	case AuthStyleBasic:
		return "basic"
	default:
		return fmt.Sprintf("AuthStyle(%d)", uint8(a))
	}
}

// Discovery is how a profile's server leads from its endpoint to the
// account's calendars.
type Discovery uint8

const (
	// DiscoveryUnset is the zero value and never valid.
	DiscoveryUnset Discovery = iota
	// DiscoveryPrincipal walks the three steps RFC 5397 and RFC 4791
	// define: read DAV:current-user-principal from the endpoint, read
	// CALDAV:calendar-home-set from the principal, then list the home's
	// members. It is the only walk there is so far, and exists as a value
	// so a server needing another one is a profile entry.
	DiscoveryPrincipal
)

// String returns the discovery style's name.
func (d Discovery) String() string {
	switch d {
	case DiscoveryUnset:
		return "unset"
	case DiscoveryPrincipal:
		return "principal"
	default:
		return fmt.Sprintf("Discovery(%d)", uint8(d))
	}
}

// Quirks are the ways one server departs from the specifications this
// connector reads. Every one of them is a field rather than a branch in
// the request code, so the next CalDAV server inherits none of iCloud's
// behaviour by accident.
//
// A false field is the specification's own behaviour. A profile only
// names what its server does differently.
type Quirks struct {
	// IgnoresSyncLimit reports that the server ignores the DAV:limit
	// element of a sync-collection report, so the connector never sizes a
	// page itself and reads whatever the server chose to hand over. See
	// RFC 6578 section 3.2 for the element the server ignores.
	IgnoresSyncLimit bool

	// SyncReportsCollection reports that a sync-collection response lists
	// the collection itself among its members. The connector drops a
	// response whose href is the collection, which would otherwise be
	// read as an event resource that fails to parse.
	SyncReportsCollection bool

	// NoFreeBusyReport reports that the server refuses the
	// CALDAV:free-busy-query report of RFC 4791 section 7.10. calbridge
	// does not use that report, so the field records what was measured
	// rather than steering a request.
	NoFreeBusyReport bool

	// RequiresEventEnd reports that the server refuses an event carrying
	// no DTEND or DURATION. The connector always writes an end, because
	// provider.Event always carries one, so the field records the
	// measurement and keeps a future profile from assuming otherwise.
	RequiresEventEnd bool

	// NoIfMatchWildcard reports that the server mishandles an If-Match
	// header whose value is "*". The connector then writes an update with
	// a concrete entity tag or with no If-Match at all.
	NoIfMatchWildcard bool

	// NoAllPropResourceType reports that the server leaves
	// DAV:resourcetype out of a DAV:allprop response. The connector asks
	// for every property by name, so the field records the measurement.
	NoAllPropResourceType bool

	// PrincipalDepthZeroOnly reports that the server refuses a PROPFIND
	// on the principal with a Depth of 1. The connector reads the
	// principal with Depth 0 either way, so the field records the
	// measurement.
	PrincipalDepthZeroOnly bool
}

// Profile is one CalDAV server, as far as this connector has to know it:
// where it starts, how it authenticates, how it leads to the calendars,
// which hosts its own answers may send the connector to, what it can do,
// and where it departs from the specifications.
//
// A second CalDAV server is an entry in Profiles and nothing else. No
// request in this package reads a profile name to decide anything.
type Profile struct {
	// Name identifies the profile and is what a credential names. It has
	// the shape of a provider id, so it is safe in a database key and a
	// metric label.
	Name string

	// DisplayName is what a user sees when they pick the server.
	DisplayName string

	// Endpoint is the absolute URL the discovery walk starts at.
	Endpoint string

	// Discovery is how the walk gets from Endpoint to the calendars.
	Discovery Discovery

	// Auth is how a request authenticates.
	Auth AuthStyle

	// HostSuffixes are the hosts the connector follows a discovered URL
	// to, as suffixes of the host name, each one written with its leading
	// dot. iCloud answers the discovery walk with an absolute URL on an
	// account specific partition host, so a connector that follows no
	// other host cannot reach the calendars at all. Keeping the set on
	// the profile is what stops a server from pointing the connector,
	// with the account's own credential, at a host of its choosing.
	HostSuffixes []string

	// Capabilities is what this server can do, declared for the whole
	// profile. It is returned unchanged from Connector.Capabilities, so a
	// difference between two CalDAV servers is a value here.
	Capabilities provider.Capabilities

	// Quirks is where this server departs from the specifications.
	Quirks Quirks
}

// Validate reports whether the profile is complete enough to drive a
// request. A profile is a constant in this package, so a gap is a
// programming error caught by this package's own tests.
func (p Profile) Validate() error {
	switch {
	case p.Name == "":
		return fmt.Errorf("profile carries no name: %w", ErrInvalidProfile)
	case p.Endpoint == "":
		return fmt.Errorf("profile %q carries no endpoint: %w", p.Name, ErrInvalidProfile)
	case !strings.HasPrefix(p.Endpoint, "https://"):
		return fmt.Errorf("profile %q endpoint %q is not https: %w", p.Name, p.Endpoint, ErrInvalidProfile)
	case p.Discovery == DiscoveryUnset:
		return fmt.Errorf("profile %q names no discovery style: %w", p.Name, ErrInvalidProfile)
	case p.Auth == AuthStyleUnset:
		return fmt.Errorf("profile %q names no authentication style: %w", p.Name, ErrInvalidProfile)
	case len(p.HostSuffixes) == 0:
		return fmt.Errorf("profile %q names no host suffix: %w", p.Name, ErrInvalidProfile)
	}
	for _, suffix := range p.HostSuffixes {
		if !strings.HasPrefix(suffix, ".") {
			return fmt.Errorf("profile %q host suffix %q does not start with a dot: %w", p.Name, suffix, ErrInvalidProfile)
		}
	}
	if p.Capabilities.PushNotifications {
		return fmt.Errorf("profile %q declares push notifications, which CalDAV has no mechanism for: %w", p.Name, ErrInvalidProfile)
	}

	return nil
}

// allowsHost reports whether the connector may send a request carrying
// this profile's credential to the given host.
func (p Profile) allowsHost(host string) bool {
	host = strings.ToLower(host)
	if at := strings.LastIndex(host, ":"); at != -1 && !strings.Contains(host[at:], "]") {
		host = host[:at]
	}
	for _, suffix := range p.HostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}

	return false
}

// ICloud is the profile for Apple's calendar service, and the first one
// this connector ships.
//
// Apple publishes no OAuth flow and no calendar REST API, so the account
// name is an Apple ID and the password is an app specific password the
// user generates by hand. Every quirk below was read off the measured
// profile the python-caldav project keeps for this server, which is
// produced by running its server tester against a real iCloud account:
// https://github.com/python-caldav/caldav/blob/master/caldav/compatibility_hints.py
var ICloud = Profile{
	Name:        "icloud",
	DisplayName: "iCloud",
	Endpoint:    "https://caldav.icloud.com/",
	Discovery:   DiscoveryPrincipal,
	Auth:        AuthStyleBasic,
	// The discovery walk lands on an account specific partition host,
	// such as p42-caldav.icloud.com, so the connector has to follow the
	// answer to another host under the same domain.
	HostSuffixes: []string{".icloud.com"},
	Capabilities: provider.Capabilities{
		// CalDAV carries no change notification of its own: RFC 4791 adds
		// no push mechanism to WebDAV, so a change is noticed by reading
		// the collection on the reconcile interval.
		PushNotifications: false,
		// A CalDAV resource is an iCalendar object stored as written, so
		// an RRULE survives the round trip as the RFC 5545 line it is.
		NativeRecurrence: true,
		// MKCALENDAR of RFC 4791 section 5.3.1 is only RECOMMENDED, and a
		// hosted service may leave it out. iCloud answers it, which is why
		// the measured profile records no departure from the library's
		// "supported" default for calendar creation.
		CalendarCreation: true,
		// A CalDAV collection may implement the scheduling extension of
		// RFC 6638, which turns an ATTENDEE written into an event into a
		// real invitation sent to a real address. Whether iCloud does that
		// for an event this connector writes is not established, so the
		// profile declares the capability it verified, and the engine
		// drops the attendee list rather than mailing strangers.
		Attendees: false,
		// RFC 5545 section 3.3.5 form one is a date-time with no zone, and
		// the server stores the object as written.
		FloatingTimes: true,
	},
	Quirks: Quirks{
		IgnoresSyncLimit:       true,
		SyncReportsCollection:  true,
		NoFreeBusyReport:       true,
		RequiresEventEnd:       true,
		NoIfMatchWildcard:      true,
		NoAllPropResourceType:  true,
		PrincipalDepthZeroOnly: true,
	},
}

// Profiles are the CalDAV servers this connector knows, keyed by profile
// name. Adding a server is an entry here.
var Profiles = map[string]Profile{
	ICloud.Name: ICloud,
}

// ProfileByName returns the profile the name identifies.
func ProfileByName(name string) (Profile, error) {
	p, ok := Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("caldav profile %q: %w", name, ErrUnknownProfile)
	}

	return p, nil
}
