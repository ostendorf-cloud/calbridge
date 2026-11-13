// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// The three PROPFIND bodies the discovery walk sends. Each one names the
// properties it wants, because a DAV:allprop answer leaves
// DAV:resourcetype out on some servers and carries the whole calendar
// object store's worth of properties on others.
const (
	propfindPrincipal = `<propfind xmlns="DAV:"><prop><current-user-principal/></prop></propfind>`

	propfindHome = `<propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">` +
		`<prop><C:calendar-home-set/></prop></propfind>`

	propfindCalendars = `<propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">` +
		`<prop>` +
		`<resourcetype/><displayname/><current-user-privilege-set/>` +
		`<C:calendar-description/><C:calendar-timezone/><C:supported-calendar-component-set/>` +
		`</prop></propfind>`
)

// contentTypeXML is the media type RFC 4918 section 8.2 gives a WebDAV
// request body.
const contentTypeXML = `application/xml; charset="utf-8"`

// managedPrefix marks a collection calbridge created. CalDAV has no
// property that says who owns a calendar, and a dead property of
// calbridge's own would depend on the server keeping one. The last path
// segment of a collection calbridge created carries this prefix instead,
// which every server keeps because it is the address the collection was
// created at.
const managedPrefix = "calbridge-"

// Calendars lists the calendars the credential reaches.
func (c *Connector) Calendars(ctx context.Context, cred provider.Credential) ([]provider.Calendar, error) {
	p, own, err := c.profileFor(cred)
	if err != nil {
		return nil, err
	}
	home, err := c.calendarHome(ctx, p, own)
	if err != nil {
		return nil, err
	}

	return c.calendarsIn(ctx, p, own, home)
}

// calendarHome walks from the profile's endpoint to the collection that
// holds the account's calendars. iCloud answers with an absolute URL on
// an account specific host, which is why every href goes through resolve
// before the credential follows it.
func (c *Connector) calendarHome(ctx context.Context, p Profile, cred Credential) (*url.URL, error) {
	endpoint, err := url.Parse(p.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("caldav profile %q endpoint: %w", p.Name, ErrInvalidProfile)
	}

	principal, err := c.propfindOne(ctx, p, cred, endpoint, "0", propfindPrincipal, func(prop davProp) string {
		if prop.CurrentUserPrincipal == nil {
			return ""
		}

		return prop.CurrentUserPrincipal.Href
	}, "DAV:current-user-principal")
	if err != nil {
		return nil, err
	}

	return c.propfindOne(ctx, p, cred, principal, "0", propfindHome, func(prop davProp) string {
		if prop.CalendarHomeSet == nil || len(prop.CalendarHomeSet.Href) == 0 {
			return ""
		}

		return prop.CalendarHomeSet.Href[0]
	}, "CALDAV:calendar-home-set")
}

// propfindOne reads one href valued property off one resource and
// resolves it. The property name is only used to say what was missing.
func (c *Connector) propfindOne(
	ctx context.Context,
	p Profile,
	cred Credential,
	at *url.URL,
	depth string,
	body string,
	pick func(davProp) string,
	property string,
) (*url.URL, error) {
	res, err := c.do(ctx, p, cred, request{
		method:      "PROPFIND",
		url:         at,
		headers:     map[string]string{"Depth": depth},
		body:        []byte(body),
		contentType: contentTypeXML,
	})
	if err != nil {
		return nil, err
	}
	if err := expect(res, "PROPFIND", http.StatusMultiStatus); err != nil {
		return nil, err
	}
	ms, err := parseMultistatus(res, "PROPFIND")
	if err != nil {
		return nil, err
	}

	for _, r := range ms.Responses {
		if href := pick(r.prop()); href != "" {
			return resolve(p, at, href)
		}
	}

	return nil, fmt.Errorf("caldav server answered no %s for the account: %w", property, ErrServer)
}

// calendarsIn lists the calendar collections inside the home.
func (c *Connector) calendarsIn(ctx context.Context, p Profile, cred Credential, home *url.URL) ([]provider.Calendar, error) {
	res, err := c.do(ctx, p, cred, request{
		method:      "PROPFIND",
		url:         home,
		headers:     map[string]string{"Depth": "1"},
		body:        []byte(propfindCalendars),
		contentType: contentTypeXML,
	})
	if err != nil {
		return nil, err
	}
	if err := expect(res, "PROPFIND", http.StatusMultiStatus); err != nil {
		return nil, err
	}
	ms, err := parseMultistatus(res, "PROPFIND")
	if err != nil {
		return nil, err
	}

	out := make([]provider.Calendar, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		prop := r.prop()
		if !prop.ResourceType.isCalendar() || !prop.SupportedComponents.holdsEvents() {
			continue
		}
		at, err := resolve(p, home, r.Href)
		if err != nil {
			return nil, err
		}
		if sameCollection(at, home) {
			continue
		}
		out = append(out, provider.Calendar{
			ID:          collectionID(at),
			Name:        prop.DisplayName,
			Description: prop.CalendarDescription,
			TimeZone:    timezoneName(prop.CalendarTimeZone),
			ReadOnly:    !prop.CurrentUserPrivilegeSet.writable(),
			// CalDAV names no primary calendar: RFC 4791 has no such
			// property, so the user picks one and this stays false.
			Primary: false,
			Managed: isManaged(at),
		})
	}
	slices.SortFunc(out, func(a, b provider.Calendar) int { return strings.Compare(a.ID, b.ID) })

	return out, nil
}

// collectionID returns the id a calendar is named by above this package,
// which is its absolute URL with a trailing slash.
func collectionID(at *url.URL) string {
	out := *at
	if !strings.HasSuffix(out.Path, "/") {
		out.Path += "/"
	}

	return out.String()
}

// sameCollection reports whether two URLs name the same collection,
// comparing the path with its trailing slash normalised.
func sameCollection(a, b *url.URL) bool {
	return a.Host == b.Host && strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/")
}

// isManaged reports whether calbridge created the collection, which it
// records in the name it created the collection under.
func isManaged(at *url.URL) bool {
	return strings.HasPrefix(lastSegment(at.Path), managedPrefix)
}

// lastSegment returns the final non-empty path segment, unescaped where
// it can be.
func lastSegment(path string) string {
	trimmed := strings.TrimSuffix(path, "/")
	at := strings.LastIndex(trimmed, "/")
	segment := trimmed[at+1:]
	if unescaped, err := url.PathUnescape(segment); err == nil {
		return unescaped
	}

	return segment
}
