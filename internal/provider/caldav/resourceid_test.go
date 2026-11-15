// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// resourceNames are the shapes a server may name an object resource,
// where the separator internal/provider reserves and the escape that
// hides it are concerned. A resource name is the server's to choose, so
// each of them has to reach an Event.ID and come back unchanged.
var resourceNames = map[string]string{
	"neither the separator nor the escape": "probe-20260301.ics",
	"the separator":                        "a#b.ics",
	"the escape":                           "a%b.ics",
	"both":                                 "a%23b.ics",
}

func TestResourceURL_RoundTripsAResourceNameThroughAnEventID(t *testing.T) {
	t.Parallel()

	collection, err := url.Parse("https://p42-caldav.icloud.com/20241234/calendars/home/")
	if err != nil {
		t.Fatalf("parse collection: %v", err)
	}

	for name, resourceName := range resourceNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			events, err := decodeResource(resourceName, wrap("UID:probe\r\nDTSTART:20260301T170000Z\r\nDTEND:20260301T190000Z\r\n"))
			if err != nil {
				t.Fatalf("decode resource: %v", err)
			}
			eventID := events[0].ID

			at, got, err := resourceURL(collection, eventID)
			if err != nil {
				t.Fatalf("resourceURL(%q) error = %v", eventID, err)
			}
			if got != resourceName {
				t.Errorf("the resource name came back as %q, want %q", got, resourceName)
			}
			if inPath := lastSegment(at.Path); inPath != resourceName {
				t.Errorf("the URL names the resource %q, want %q", inPath, resourceName)
			}
			if provider.IsInstanceIDOf(eventID, "a") {
				t.Errorf("the resource %q reads as an occurrence of an event named \"a\"", resourceName)
			}
		})
	}
}

func TestResourceURL_RefusesAnIDDerivedForAnOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	// A derived id names no resource of its own: the override lives
	// inside the master's object, and the only name the server has for it
	// is the master's.
	collection, err := url.Parse("https://p42-caldav.icloud.com/20241234/calendars/home/")
	if err != nil {
		t.Fatalf("parse collection: %v", err)
	}

	derived := provider.InstanceIDOf(provider.EventIDOf("a#b.ics"), "20260304T090000")

	if _, _, err := resourceURL(collection, derived); err == nil {
		t.Errorf("resourceURL(%q) built a URL for an id that names no resource", derived)
	}
}

// answerWith is the HTTP surface of a test that replays one response.
type answerWith func(*http.Request) (*http.Response, error)

func (f answerWith) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestReadEvent_NamesTheEventAfterTheResourceAndNotAfterTheIDItWasAskedFor(t *testing.T) {
	t.Parallel()

	// readEvent is handed an Event.ID and decodeResource is handed a
	// resource name, so the id has to be decoded exactly once on the way
	// between them. Escaping it a second time yields an id no other read
	// path of this connector produces, and the caller's own fields say
	// nothing about it.
	const resourceName = "a#b.ics"

	var asked string
	connector := New(WithClient(answerWith(func(req *http.Request) (*http.Response, error) {
		asked = req.URL.Path
		header := http.Header{}
		header.Set("ETag", `"C=12@U=20241234/1"`)
		body := wrap("UID:probe\r\nDTSTART:20260301T170000Z\r\nDTEND:20260301T190000Z\r\n")

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})))

	cred, err := NewCredential("conn-1", "icloud", "anna.beispiel@example.com", "aaaa-bbbb-cccc-dddd")
	if err != nil {
		t.Fatalf("build credential: %v", err)
	}
	p, own, err := connector.profileFor(cred)
	if err != nil {
		t.Fatalf("read the profile: %v", err)
	}
	calendar, err := parseCalendarURL(p, "https://p42-caldav.icloud.com:443/20241234/calendars/home/")
	if err != nil {
		t.Fatalf("parse the calendar: %v", err)
	}

	eventID := provider.EventIDOf(resourceName)

	held, err := connector.readEvent(t.Context(), p, own, calendar, eventID)
	if err != nil {
		t.Fatalf("readEvent(%q) error = %v", eventID, err)
	}
	if want := "/20241234/calendars/home/" + resourceName; asked != want {
		t.Errorf("the connector read %q, want %q", asked, want)
	}
	if held.ID != eventID {
		t.Errorf("readEvent(%q) returned the event under id %q, want the id it was asked for", eventID, held.ID)
	}
}

func TestDecodeResource_NamesAnOverrideAfterTheEscapedResourceName(t *testing.T) {
	t.Parallel()

	// Both halves of one resource go through the same escaping, so the
	// master's id is what the override's id is built from and what
	// Recurrence.MasterID names.
	const resourceName = "a#b.ics"
	object := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:probe\r\nDTSTART:20260304T090000Z\r\nDTEND:20260304T100000Z\r\n" +
		"RRULE:FREQ=WEEKLY;COUNT=3\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:probe\r\nRECURRENCE-ID:20260311T090000Z\r\n" +
		"DTSTART:20260311T140000Z\r\nDTEND:20260311T150000Z\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	events, err := decodeResource(resourceName, object)
	if err != nil {
		t.Fatalf("decode resource: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("the resource decoded into %d events, want the master and its override", len(events))
	}
	master, override := events[0], events[1]

	wantMasterID := provider.EventIDOf(resourceName)
	if master.ID != wantMasterID {
		t.Errorf("master id = %q, want %q", master.ID, wantMasterID)
	}
	if override.Recurrence.MasterID != wantMasterID {
		t.Errorf("the override names master %q, want %q", override.Recurrence.MasterID, wantMasterID)
	}
	if !provider.IsInstanceIDOf(override.ID, master.ID) {
		t.Errorf("the override %q is not an instance id of its master %q", override.ID, master.ID)
	}
	if provider.IsInstanceIDOf(master.ID, "a") {
		t.Errorf("the master %q reads as an occurrence of an event named \"a\"", master.ID)
	}
}
