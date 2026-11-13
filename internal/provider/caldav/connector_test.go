// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/provider/caldav"
)

// The account and the password the tests authenticate with. Both are
// placeholders in the shape iCloud uses, and neither reaches a server.
const (
	testAccount  = "anna.beispiel@example.com"
	testPassword = "aaaa-bbbb-cccc-dddd"
)

// The two collections the recorded answers describe.
const (
	homeCalendar    = "https://p42-caldav.icloud.com:443/20241234/calendars/home/"
	mirrorCalendar  = "https://p42-caldav.icloud.com:443/20241234/calendars/calbridge-7f2c/"
	foreignCalendar = "https://p42-caldav.icloud.com:443/20241234/calendars/veranstaltungstechnik/"
)

// fixedNow is the clock every test writes with, so a written object is
// the same on every run.
var fixedNow = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

// newConnector returns a connector answered by the given server.
func newConnector(t *testing.T, server *fakeServer) *caldav.Connector {
	t.Helper()

	return caldav.New(
		caldav.WithClient(server),
		caldav.WithClock(func() time.Time { return fixedNow }),
		caldav.WithUIDs(func() string { return "7f2c" }),
	)
}

// testCredential returns the credential the tests authenticate with.
func testCredential(t *testing.T) caldav.Credential {
	t.Helper()

	cred, err := caldav.NewCredential("conn-1", "icloud", testAccount, testPassword)
	if err != nil {
		t.Fatalf("build credential: %v", err)
	}

	return cred
}

func TestConnector_Capabilities(t *testing.T) {
	t.Parallel()

	caps := caldav.New().Capabilities()

	if caps.PushNotifications {
		t.Error("CalDAV has no push mechanism, so the connector must not declare one")
	}
	if !caps.NativeRecurrence {
		t.Error("a CalDAV resource holds RFC 5545 recurrence rules as written")
	}
	if !caps.CalendarCreation {
		t.Error("the iCloud profile answers MKCALENDAR")
	}
	if caps.Attendees {
		t.Error("the iCloud profile declares no attendee writing, because a write may trigger scheduling")
	}
	if !caps.FloatingTimes {
		t.Error("RFC 5545 stores a wall time with no zone, and the server keeps the object as written")
	}
}

func TestConnector_Calendars(t *testing.T) {
	t.Parallel()

	server := discoverable(t).
		on("PROPFIND", "/20241234/calendars/", multistatus(recordedBody(t, "calendars.xml")))
	connector := newConnector(t, server)

	calendars, err := connector.Calendars(t.Context(), testCredential(t))
	if err != nil {
		t.Fatalf("list calendars: %v", err)
	}

	want := []provider.Calendar{
		{
			ID:       mirrorCalendar,
			Name:     "Arbeit gespiegelt",
			ReadOnly: false,
			Managed:  true,
		},
		{
			ID:          homeCalendar,
			Name:        "Privat",
			Description: "Termine der Familie",
			TimeZone:    "Europe/Berlin",
		},
		{
			ID:       foreignCalendar,
			Name:     "Veranstaltungstechnik",
			ReadOnly: true,
		},
	}
	if len(calendars) != len(want) {
		t.Fatalf("got %d calendars, want %d: %+v", len(calendars), len(want), calendars)
	}
	for i, w := range want {
		if calendars[i] != w {
			t.Errorf("calendar %d\n got %+v\nwant %+v", i, calendars[i], w)
		}
	}
}

func TestConnector_CalendarsFollowsThePartitionHost(t *testing.T) {
	t.Parallel()

	server := discoverable(t).
		on("PROPFIND", "/20241234/calendars/", multistatus(recordedBody(t, "calendars.xml")))

	if _, err := newConnector(t, server).Calendars(t.Context(), testCredential(t)); err != nil {
		t.Fatalf("list calendars: %v", err)
	}

	calls := server.recorded()
	if got := calls[0].host; got != "caldav.icloud.com" {
		t.Errorf("discovery started at %q, want caldav.icloud.com", got)
	}
	if got := calls[len(calls)-1].host; got != "p42-caldav.icloud.com:443" {
		t.Errorf("calendar listing went to %q, want the partition host the home set named", got)
	}
}

func TestConnector_CalendarsRefusesAHomeOutsideTheProfile(t *testing.T) {
	t.Parallel()

	const elsewhere = `<?xml version="1.0" encoding="UTF-8"?>
<multistatus xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <response>
    <href>/20241234/principal/</href>
    <propstat>
      <prop><C:calendar-home-set><href>https://collector.example.net/calendars/</href></C:calendar-home-set></prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`

	server := newFakeServer().
		on("PROPFIND", "/", multistatus(recordedBody(t, "principal.xml"))).
		on("PROPFIND", "/20241234/principal/", multistatus(elsewhere))

	_, err := newConnector(t, server).Calendars(t.Context(), testCredential(t))
	if !errors.Is(err, caldav.ErrForbiddenHost) {
		t.Fatalf("got %v, want the profile's host suffix check to refuse it", err)
	}
	for _, c := range server.recorded() {
		if c.host == "collector.example.net" {
			t.Fatal("the credential was sent to a host the profile does not allow")
		}
	}
}

func TestConnector_CalendarsReportsARefusedCredential(t *testing.T) {
	t.Parallel()

	server := newFakeServer().on("PROPFIND", "/", answer{status: http.StatusUnauthorized, body: ""})

	_, err := newConnector(t, server).Calendars(t.Context(), testCredential(t))
	if !errors.Is(err, caldav.ErrAuthentication) {
		t.Fatalf("got %v, want ErrAuthentication", err)
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatal("the error carries the app specific password")
	}
}

func TestConnector_ChangesInitialSync(t *testing.T) {
	t.Parallel()

	server := newFakeServer().
		on("REPORT", "/20241234/calendars/home/", multistatus(recordedBody(t, "sync-complete.xml")))
	connector := newConnector(t, server)

	page, err := connector.Changes(t.Context(), testCredential(t), homeCalendar, provider.SyncToken{})
	if err != nil {
		t.Fatalf("read changes: %v", err)
	}
	if page.State != provider.PageStateComplete {
		t.Errorf("state is %s, want complete", page.State)
	}
	if page.Token.Value != "HwoQEgwAAAAAAAAAAAAAABgCGgQJ3OPzGgQJ3OP0" {
		t.Errorf("token is %q, want the one the report carried", page.Token.Value)
	}
}

// changesFor replays a sync report and the multiget that follows it.
func changesFor(t *testing.T, report string, token provider.SyncToken) (provider.ChangePage, *fakeServer) {
	t.Helper()

	server := newFakeServer()
	calls := 0
	server.on("REPORT", "/20241234/calendars/home/", answer{})
	connector := caldav.New(
		caldav.WithClient(doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == "REPORT" {
				calls++
				if calls == 1 {
					return server.answerWith(req, multistatus(recordedBody(t, report)))
				}

				return server.answerWith(req, multistatus(recordedBody(t, "multiget.xml")))
			}

			return server.Do(req)
		})),
		caldav.WithClock(func() time.Time { return fixedNow }),
	)

	page, err := connector.Changes(t.Context(), testCredential(t), homeCalendar, token)
	if err != nil {
		t.Fatalf("read changes: %v", err)
	}

	return page, server
}

func TestConnector_ChangesReportsEveryEventAndEveryRemoval(t *testing.T) {
	t.Parallel()

	page, _ := changesFor(t, "sync-complete.xml", provider.SyncToken{})

	if page.State != provider.PageStateComplete {
		t.Fatalf("state is %s, want complete", page.State)
	}
	if err := page.Validate(); err != nil {
		t.Fatalf("the page the connector built is not one a caller can act on: %v", err)
	}

	want := []struct {
		kind provider.ChangeKind
		id   string
	}{
		{provider.ChangeKindUpsert, "probe-20260301.ics"},
		{provider.ChangeKindUpsert, "standup.ics"},
		{provider.ChangeKindUpsert, "standup.ics#20260112T090000"},
		{provider.ChangeKindDelete, "abgesagt.ics"},
	}
	if len(page.Changes) != len(want) {
		t.Fatalf("got %d changes, want %d", len(page.Changes), len(want))
	}
	for i, w := range want {
		if page.Changes[i].Kind != w.kind || page.Changes[i].EventID != w.id {
			t.Errorf("change %d is %s %q, want %s %q",
				i, page.Changes[i].Kind, page.Changes[i].EventID, w.kind, w.id)
		}
	}
}

func TestConnector_ChangesMapsOneEvent(t *testing.T) {
	t.Parallel()

	page, _ := changesFor(t, "sync-complete.xml", provider.SyncToken{})
	event := page.Changes[0].Event

	if event.UID != "probe-20260301" {
		t.Errorf("uid is %q", event.UID)
	}
	if event.Summary != "Probe Hauptbuehne" {
		t.Errorf("summary is %q", event.Summary)
	}
	if event.Location != "Stadthalle, Saal 2" {
		t.Errorf("location is %q, want the escaped comma read back", event.Location)
	}
	if !strings.Contains(event.Description, "\n") {
		t.Errorf("description is %q, want the escaped line break read back", event.Description)
	}
	if event.Visibility != provider.VisibilityPrivate {
		t.Errorf("visibility is %s", event.Visibility)
	}
	if event.ETag != `"C=12@U=20241234/1"` {
		t.Errorf("etag is %q", event.ETag)
	}
	want := provider.Zoned(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC), "Europe/Berlin")
	if !event.Start.Equal(want) {
		t.Errorf("start is %s, want %s", event.Start, want)
	}
}

func TestConnector_ChangesMapsARecurringMasterAndItsOverride(t *testing.T) {
	t.Parallel()

	page, _ := changesFor(t, "sync-complete.xml", provider.SyncToken{})
	master, override := page.Changes[1].Event, page.Changes[2].Event

	if !master.Recurrence.IsMaster() {
		t.Fatal("the master carries no recurrence rule")
	}
	wantRule := []string{
		"RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"EXDATE;TZID=Europe/Berlin:20260406T090000",
	}
	if strings.Join(master.Recurrence.Rule, "|") != strings.Join(wantRule, "|") {
		t.Errorf("rule is %q, want %q", master.Recurrence.Rule, wantRule)
	}
	if master.Marker != (provider.Marker{Edge: "edge-17", Source: "google-evt-991"}) {
		t.Errorf("marker is %+v, want the one calbridge wrote", master.Marker)
	}
	if override.Recurrence.MasterID != "standup.ics" {
		t.Errorf("the override names master %q", override.Recurrence.MasterID)
	}
	wantOriginal := provider.Zoned(time.Date(2026, time.January, 12, 9, 0, 0, 0, time.UTC), "Europe/Berlin")
	if !override.Recurrence.OriginalStart.Equal(wantOriginal) {
		t.Errorf("original start is %s, want %s", override.Recurrence.OriginalStart, wantOriginal)
	}
	if override.Recurrence.IsMaster() {
		t.Error("an overridden occurrence carries no rule of its own")
	}
}

func TestConnector_ChangesResumesATruncatedResponse(t *testing.T) {
	t.Parallel()

	page, server := changesFor(t, "sync-truncated.xml", provider.SyncToken{Value: "earlier"})

	if page.State != provider.PageStateResumable {
		t.Fatalf("state is %s, want resumable: the token covers exactly what was delivered", page.State)
	}
	if !page.State.More() {
		t.Error("a truncated response leaves more to hand over")
	}
	if !page.State.Storable() {
		t.Error("the token of a truncated response is a position worth storing")
	}
	if page.Token.Value != "HwoQEgwAAAAAAAAAAAAAABgCGgQJ3OPzGgQJ3OP1" {
		t.Errorf("token is %q, want the one the truncated report carried", page.Token.Value)
	}
	multiget := server.last(t, "REPORT").body
	if got := strings.Count(multiget, "<href"); got != 1 {
		t.Errorf("the multiget named %d resources, want only the one the truncated report handed over", got)
	}
	if !strings.Contains(multiget, "probe-20260301.ics") {
		t.Errorf("the multiget did not name the resource the report handed over:\n%s", multiget)
	}
}

func TestConnector_ChangesRestartsOnARejectedToken(t *testing.T) {
	t.Parallel()

	server := newFakeServer().on("REPORT", "/20241234/calendars/home/", answer{
		status: http.StatusForbidden,
		body:   recordedBody(t, "sync-invalid-token.xml"),
	})

	_, err := newConnector(t, server).
		Changes(t.Context(), testCredential(t), homeCalendar, provider.SyncToken{Value: "stale"})
	if !errors.Is(err, provider.ErrSyncTokenInvalid) {
		t.Fatalf("got %v, want ErrSyncTokenInvalid from the DAV:valid-sync-token precondition", err)
	}
}

func TestConnector_ChangesKeepsAnUnrelatedRefusalApart(t *testing.T) {
	t.Parallel()

	const otherPrecondition = `<?xml version="1.0" encoding="UTF-8"?>
<error xmlns="DAV:"><need-privileges/></error>`

	server := newFakeServer().on("REPORT", "/20241234/calendars/home/", answer{
		status: http.StatusForbidden,
		body:   otherPrecondition,
	})

	_, err := newConnector(t, server).
		Changes(t.Context(), testCredential(t), homeCalendar, provider.SyncToken{Value: "stale"})
	if errors.Is(err, provider.ErrSyncTokenInvalid) {
		t.Fatal("a refusal naming another precondition must not look like a rejected token")
	}
	if !errors.Is(err, caldav.ErrServer) {
		t.Fatalf("got %v, want ErrServer", err)
	}
}

func TestConnector_CreateEvent(t *testing.T) {
	t.Parallel()

	server := newFakeServer().on(http.MethodPut, "/20241234/calendars/home/7f2c.ics",
		written(http.StatusCreated, `"C=13@U=20241234/1"`))

	event := provider.Event{
		Summary: "Lastenaufzug reservieren",
		Start:   provider.Zoned(time.Date(2026, time.April, 2, 8, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:     provider.Zoned(time.Date(2026, time.April, 2, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Marker:  provider.Marker{Edge: "edge-17", Source: "google-evt-77"},
	}

	stored, err := newConnector(t, server).CreateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if stored.ID != "7f2c.ics" || stored.UID != "7f2c" {
		t.Errorf("stored id %q and uid %q", stored.ID, stored.UID)
	}
	if stored.ETag != `"C=13@U=20241234/1"` {
		t.Errorf("etag is %q, want the one the server returned", stored.ETag)
	}
	if !stored.Created.Equal(fixedNow) || !stored.Updated.Equal(fixedNow) {
		t.Errorf("created %s and updated %s, want the moment of the write", stored.Created, stored.Updated)
	}

	put := server.last(t, http.MethodPut)
	if got := put.header.Get("If-None-Match"); got != "*" {
		t.Errorf("If-None-Match is %q, want * so an existing resource is not overwritten", got)
	}
	for _, want := range []string{
		"BEGIN:VTIMEZONE",
		"TZID:Europe/Berlin",
		"DTSTART;TZID=Europe/Berlin:20260402T080000",
		"DTEND;TZID=Europe/Berlin:20260402T100000",
		"X-CALBRIDGE-EDGE:edge-17",
	} {
		if !strings.Contains(put.body, want) {
			t.Errorf("the written object carries no %q:\n%s", want, put.body)
		}
	}
}

func TestConnector_CreateEventDropsAttendees(t *testing.T) {
	t.Parallel()

	server := newFakeServer().on(http.MethodPut, "/20241234/calendars/home/7f2c.ics",
		written(http.StatusCreated, `"C=13@U=20241234/2"`))

	event := provider.Event{
		Summary:   "Abstimmung",
		Start:     provider.Zoned(time.Date(2026, time.April, 3, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:       provider.Zoned(time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Attendees: []provider.Attendee{{Email: "jonas.weber@example.com", Name: "Jonas Weber"}},
	}

	stored, err := newConnector(t, server).CreateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if len(stored.Attendees) != 0 {
		t.Errorf("the connector kept %d attendees it declares it does not write", len(stored.Attendees))
	}
	if strings.Contains(server.last(t, http.MethodPut).body, "ATTENDEE") {
		t.Error("the written object carries an ATTENDEE, which the declared capability rules out")
	}
}

func TestConnector_CreateEventKeepsAFloatingTime(t *testing.T) {
	t.Parallel()

	server := newFakeServer().on(http.MethodPut, "/20241234/calendars/home/7f2c.ics",
		written(http.StatusCreated, `"C=13@U=20241234/3"`))

	event := provider.Event{
		Summary: "Zeitumstellung pruefen",
		Start:   provider.Floating(time.Date(2026, time.October, 25, 3, 0, 0, 0, time.UTC)),
		End:     provider.Floating(time.Date(2026, time.October, 25, 4, 0, 0, 0, time.UTC)),
	}

	if _, err := newConnector(t, server).CreateEvent(t.Context(), testCredential(t), homeCalendar, event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	put := server.last(t, http.MethodPut)
	if !strings.Contains(put.body, "DTSTART:20261025T030000\r\n") {
		t.Errorf("a floating time was not written as a date-time with no zone:\n%s", put.body)
	}
	if strings.Contains(put.body, "BEGIN:VTIMEZONE") {
		t.Error("a floating time names no zone, so the object needs no VTIMEZONE")
	}
}

func TestConnector_UpdateEvent(t *testing.T) {
	t.Parallel()

	held := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iOS 18.1//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:probe-20260301\r\nDTSTAMP:20260228T101500Z\r\n" +
		"CREATED:20260101T090000Z\r\nDTSTART:20260301T180000Z\r\nDTEND:20260301T223000Z\r\n" +
		"SUMMARY:Probe Hauptbuehne\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	server := newFakeServer().
		on(http.MethodGet, "/20241234/calendars/home/probe-20260301.ics", read(held, `"C=12@U=20241234/1"`)).
		on(http.MethodPut, "/20241234/calendars/home/probe-20260301.ics",
			written(http.StatusNoContent, `"C=14@U=20241234/1"`))

	event := provider.Event{
		ID:      "probe-20260301.ics",
		Summary: "Probe Hauptbuehne, verlegt",
		Start:   provider.Zoned(time.Date(2026, time.March, 1, 19, 0, 0, 0, time.UTC), "UTC"),
		End:     provider.Zoned(time.Date(2026, time.March, 1, 23, 0, 0, 0, time.UTC), "UTC"),
	}

	stored, err := newConnector(t, server).UpdateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if err != nil {
		t.Fatalf("update event: %v", err)
	}
	if want := time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC); !stored.Created.Equal(want) {
		t.Errorf("created is %s, want the original write at %s", stored.Created, want)
	}
	if !stored.Updated.Equal(fixedNow) {
		t.Errorf("updated is %s, want the moment of this write", stored.Updated)
	}
	if stored.UID != "probe-20260301" {
		t.Errorf("uid is %q, want the one the resource already held", stored.UID)
	}
	if got := server.last(t, http.MethodPut).header.Get("If-Match"); got != `"C=12@U=20241234/1"` {
		t.Errorf("If-Match is %q, want the entity tag the resource was read at", got)
	}
}

func TestConnector_UpdateEventReportsAnIdTheCalendarDoesNotHold(t *testing.T) {
	t.Parallel()

	server := newFakeServer().
		on(http.MethodGet, "/20241234/calendars/home/weg.ics", answer{status: http.StatusNotFound})

	event := provider.Event{
		ID:    "weg.ics",
		Start: provider.Date(2026, time.May, 1),
		End:   provider.Date(2026, time.May, 2),
	}

	_, err := newConnector(t, server).UpdateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if !errors.Is(err, provider.ErrUnknownEvent) {
		t.Fatalf("got %v, want ErrUnknownEvent", err)
	}
}

func TestConnector_UpdateEventRefusesOneOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	event := provider.Event{
		ID:    "standup.ics#20260112T090000",
		Start: provider.Zoned(time.Date(2026, time.January, 12, 10, 30, 0, 0, time.UTC), "Europe/Berlin"),
		End:   provider.Zoned(time.Date(2026, time.January, 12, 11, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Recurrence: provider.Recurrence{
			MasterID:      "standup.ics",
			OriginalStart: provider.Zoned(time.Date(2026, time.January, 12, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		},
	}

	_, err := newConnector(t, newFakeServer()).
		UpdateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

func TestConnector_DeleteEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
	}{
		{name: "the server removed it", status: http.StatusNoContent},
		{name: "it was already gone", status: http.StatusNotFound},
		{name: "it was removed earlier", status: http.StatusGone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := newFakeServer().
				on(http.MethodDelete, "/20241234/calendars/home/probe-20260301.ics", answer{status: tc.status})

			err := newConnector(t, server).
				DeleteEvent(t.Context(), testCredential(t), homeCalendar, "probe-20260301.ics")
			if err != nil {
				t.Fatalf("delete event: %v", err)
			}
		})
	}
}

func TestConnector_DeleteEventRefusesANameThatLeavesTheCollection(t *testing.T) {
	t.Parallel()

	server := newFakeServer()

	err := newConnector(t, server).
		DeleteEvent(t.Context(), testCredential(t), homeCalendar, "../calbridge-7f2c/probe.ics")
	if !errors.Is(err, provider.ErrUnknownEvent) {
		t.Fatalf("got %v, want the resource name check to refuse it", err)
	}
	if len(server.recorded()) != 0 {
		t.Fatal("a request was made for an event id that leaves the collection")
	}
}

func TestConnector_CreateCalendar(t *testing.T) {
	t.Parallel()

	server := discoverable(t).
		on("MKCALENDAR", "/20241234/calendars/calbridge-7f2c/", answer{status: http.StatusCreated})

	calendar, err := newConnector(t, server).
		CreateCalendar(t.Context(), testCredential(t), "Arbeit gespiegelt")
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}
	if calendar.ID != mirrorCalendar {
		t.Errorf("id is %q, want %q", calendar.ID, mirrorCalendar)
	}
	if !calendar.Managed {
		t.Error("a calendar the connector created is one calbridge owns")
	}

	body := server.last(t, "MKCALENDAR").body
	for _, want := range []string{"Arbeit gespiegelt", `name="VEVENT"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the MKCALENDAR body carries no %q:\n%s", want, body)
		}
	}
}

func TestConnector_DeleteCalendar(t *testing.T) {
	t.Parallel()

	server := newFakeServer().
		on(http.MethodDelete, "/20241234/calendars/calbridge-7f2c/", answer{status: http.StatusNoContent})

	if err := newConnector(t, server).DeleteCalendar(t.Context(), testCredential(t), mirrorCalendar); err != nil {
		t.Fatalf("delete calendar: %v", err)
	}
}

func TestConnector_DeleteCalendarRefusesOneCalbridgeDidNotCreate(t *testing.T) {
	t.Parallel()

	server := newFakeServer()

	err := newConnector(t, server).DeleteCalendar(t.Context(), testCredential(t), homeCalendar)
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("got %v, want the managed name check to refuse it", err)
	}
	if len(server.recorded()) != 0 {
		t.Fatal("a DELETE was sent for a calendar calbridge does not own")
	}
}

func TestConnector_RefusesACredentialForAnotherProvider(t *testing.T) {
	t.Parallel()

	_, err := newConnector(t, newFakeServer()).Calendars(t.Context(), otherCredential{})
	if !errors.Is(err, provider.ErrCredentialMismatch) {
		t.Fatalf("got %v, want ErrCredentialMismatch from provider.CheckCredential", err)
	}
}

func TestConnector_RefusesNoCredential(t *testing.T) {
	t.Parallel()

	_, err := newConnector(t, newFakeServer()).Calendars(t.Context(), nil)
	if !errors.Is(err, provider.ErrCredentialMissing) {
		t.Fatalf("got %v, want ErrCredentialMissing from provider.CheckCredential", err)
	}
}

func TestConnector_StopsOnACancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	server := newFakeServer().on("PROPFIND", "/", multistatus(recordedBody(t, "principal.xml")))

	_, err := caldav.New(caldav.WithClient(realTransportFor(server))).Calendars(ctx, testCredential(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestConnector_RegistersIntoTheRegistry(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	if err := registry.Register(caldav.New()); err != nil {
		t.Fatalf("register: %v", err)
	}
	connector, err := registry.Connector(caldav.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if connector.ID() != caldav.ID {
		t.Errorf("resolved %q", connector.ID())
	}
}

// otherCredential belongs to a provider that is not this one.
type otherCredential struct{}

func (otherCredential) Provider() provider.ID { return provider.ID("google") }

func (otherCredential) Connection() string { return "conn-9" }

// doerFunc adapts a function to the connector's HTTP surface.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

// answerWith records the request and returns the given answer, so a test
// that routes by call order still sees what was sent.
func (s *fakeServer) answerWith(req *http.Request, a answer) (*http.Response, error) {
	s.on(req.Method, req.URL.Path, a)

	return s.Do(req)
}

// realTransportFor honours the request's context before answering, which
// is what a real transport does and what a cancelled call must hit.
func realTransportFor(s *fakeServer) doerFunc {
	return func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}

		return s.Do(req)
	}
}
