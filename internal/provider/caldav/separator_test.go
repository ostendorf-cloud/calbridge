// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/provider/caldav"
)

func TestConnector_DeletesAResourceWhoseNameHoldsTheSeparator(t *testing.T) {
	t.Parallel()

	// The symptom a user meets. A resource name is the server's to
	// choose, and the connector declares no instance writes, so it used
	// to refuse every id carrying the separator. A master named
	// "a#b.ics" could then not be deleted at all, on a reconcile or on a
	// teardown.
	const resourceName = "a#b.ics"

	server := newFakeServer().
		on(http.MethodDelete, "/20241234/calendars/home/"+resourceName, answer{status: http.StatusNoContent})

	eventID := provider.EventIDOf(resourceName)

	if err := newConnector(t, server).DeleteEvent(t.Context(), testCredential(t), homeCalendar, eventID); err != nil {
		t.Fatalf("DeleteEvent(%q) error = %v", eventID, err)
	}

	// The request is the only proof: a delete the connector refuses
	// leaves the calendar exactly as a delete it never sent does.
	deleted := server.last(t, http.MethodDelete)
	if want := "/20241234/calendars/home/" + resourceName; deleted.path != want {
		t.Errorf("the connector deleted %q, want %q", deleted.path, want)
	}
}

// The resource the sync reports below name, carrying the separator in
// the name the server chose for it.
const separatorResource = "a#b.ics"

// separatorChangedReport names that resource as changed, and
// separatorGoneReport names it as gone. The two halves are reported
// separately because they are reported by different branches, and a
// deletion is the half that was broken. A server escapes the separator
// in an href, because an unescaped one starts a fragment.
const separatorChangedReport = `<?xml version="1.0" encoding="UTF-8"?>
<multistatus xmlns="DAV:">
  <response>
    <href>/20241234/calendars/home/a%23b.ics</href>
    <propstat>
      <prop>
        <getetag>"C=12@U=20241234/1"</getetag>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
  <sync-token>HwoQEgwAAAAAAAAAAAAAABgCGgQJ3OPz</sync-token>
</multistatus>`

const separatorGoneReport = `<?xml version="1.0" encoding="UTF-8"?>
<multistatus xmlns="DAV:">
  <response>
    <href>/20241234/calendars/home/a%23b.ics</href>
    <status>HTTP/1.1 404 Not Found</status>
  </response>
  <sync-token>HwoQEgwAAAAAAAAAAAAAABgCGgQJ3OP0</sync-token>
</multistatus>`

// separatorMultiget hands over the one object the report named as
// changed.
const separatorMultiget = `<?xml version="1.0" encoding="UTF-8"?>
<multistatus xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <response>
    <href>/20241234/calendars/home/a%23b.ics</href>
    <propstat>
      <prop>
        <getetag>"C=12@U=20241234/1"</getetag>
        <C:calendar-data>BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Apple Inc.//iOS 18.1//EN
BEGIN:VEVENT
UID:probe
DTSTAMP:20260228T101500Z
DTSTART:20260301T170000Z
DTEND:20260301T190000Z
SUMMARY:Probe
END:VEVENT
END:VCALENDAR
</C:calendar-data>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`

// separatorChangesFor replays one sync report over a resource whose
// name carries the separator, and the multiget that follows where the
// report named it as changed.
func separatorChangesFor(t *testing.T, report string) provider.ChangePage {
	t.Helper()

	server := newFakeServer()
	reports := 0
	connector := caldav.New(
		caldav.WithClient(doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == "REPORT" {
				reports++
				if reports == 1 {
					return server.answerWith(req, multistatus(report))
				}

				return server.answerWith(req, multistatus(separatorMultiget))
			}

			return server.Do(req)
		})),
		caldav.WithClock(func() time.Time { return fixedNow }),
	)

	page, err := connector.Changes(t.Context(), testCredential(t), homeCalendar, provider.SyncToken{})
	if err != nil {
		t.Fatalf("read changes: %v", err)
	}
	if err := page.Validate(); err != nil {
		t.Fatalf("the page the connector built is not one a caller can act on: %v", err)
	}
	if len(page.Changes) != 1 {
		t.Fatalf("got %d changes, want the one the report named", len(page.Changes))
	}

	return page
}

func TestConnector_ChangesNamesAChangedResourceHoldingTheSeparatorAsTheWritePathDoes(t *testing.T) {
	t.Parallel()

	change := separatorChangesFor(t, separatorChangedReport).Changes[0]

	if change.Kind != provider.ChangeKindUpsert {
		t.Fatalf("the change is a %s, want an upsert", change.Kind)
	}
	if want := provider.EventIDOf(separatorResource); change.EventID != want {
		t.Errorf("the upsert of %q is reported as %q, want %q", separatorResource, change.EventID, want)
	}
	if change.Event.ID != change.EventID {
		t.Errorf("the change names %q and carries an event under %q", change.EventID, change.Event.ID)
	}
	assertWritesToTheResource(t, change.EventID, separatorResource)
}

func TestConnector_ChangesNamesAGoneResourceHoldingTheSeparatorAsTheWritePathDoes(t *testing.T) {
	t.Parallel()

	// This is the half that was broken. A deletion reported under the
	// server's own resource name instead of the id the upsert of that
	// resource carried is a key no mapping row is found by, so the copy
	// is never removed.
	change := separatorChangesFor(t, separatorGoneReport).Changes[0]

	if change.Kind != provider.ChangeKindDelete {
		t.Fatalf("the change is a %s, want a deletion", change.Kind)
	}
	if want := provider.EventIDOf(separatorResource); change.EventID != want {
		t.Errorf("the deletion of %q is reported as %q, want %q", separatorResource, change.EventID, want)
	}
	assertWritesToTheResource(t, change.EventID, separatorResource)
}

// assertWritesToTheResource checks that the reported id is one the write
// path turns back into the resource the server named, which is the only
// thing a caller does with it.
func assertWritesToTheResource(t *testing.T, eventID, resourceName string) {
	t.Helper()

	target := newFakeServer().
		on(http.MethodDelete, "/20241234/calendars/home/"+resourceName, answer{status: http.StatusNoContent})

	if err := newConnector(t, target).
		DeleteEvent(t.Context(), testCredential(t), homeCalendar, eventID); err != nil {
		t.Fatalf("DeleteEvent(%q) error = %v", eventID, err)
	}
	if want, got := "/20241234/calendars/home/"+resourceName, target.last(t, http.MethodDelete).path; got != want {
		t.Errorf("the id %q writes to %q, want %q", eventID, got, want)
	}
}
