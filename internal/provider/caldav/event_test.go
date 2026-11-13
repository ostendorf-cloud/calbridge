// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// wrap builds a calendar object around one VEVENT body.
func wrap(body string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\n" +
		body + "END:VEVENT\r\nEND:VCALENDAR\r\n"
}

func TestDecodeResource_Times(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want provider.EventTime
	}{
		{
			name: "an all-day start",
			body: "UID:ferien\r\nDTSTART;VALUE=DATE:20260701\r\nDTEND;VALUE=DATE:20260715\r\n",
			want: provider.Date(2026, time.July, 1),
		},
		{
			name: "a start in a named zone",
			body: "UID:probe\r\nDTSTART;TZID=Europe/Berlin:20260301T180000\r\nDTEND;TZID=Europe/Berlin:20260301T200000\r\n",
			want: provider.Zoned(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC), "Europe/Berlin"),
		},
		{
			name: "a start given as an instant",
			body: "UID:probe\r\nDTSTART:20260301T170000Z\r\nDTEND:20260301T190000Z\r\n",
			want: provider.Zoned(time.Date(2026, time.March, 1, 17, 0, 0, 0, time.UTC), "UTC"),
		},
		{
			name: "a start in no zone at all",
			body: "UID:wecker\r\nDTSTART:20260301T070000\r\nDTEND:20260301T071000\r\n",
			want: provider.Floating(time.Date(2026, time.March, 1, 7, 0, 0, 0, time.UTC)),
		},
		{
			name: "a zone neither the database nor the object knows",
			body: "UID:probe\r\nDTSTART;TZID=Customized Time Zone:20260301T180000\r\nDTEND;TZID=Customized Time Zone:20260301T190000\r\n",
			want: provider.Floating(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC)),
		},
		{
			name: "a zone name with the leading solidus",
			body: "UID:probe\r\nDTSTART;TZID=/Europe/Berlin:20260301T180000\r\nDTEND;TZID=/Europe/Berlin:20260301T190000\r\n",
			want: provider.Zoned(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC), "Europe/Berlin"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			events, err := decodeResource("probe.ics", wrap(tc.body))
			if err != nil {
				t.Fatalf("decode resource: %v", err)
			}
			if !events[0].Start.Equal(tc.want) {
				t.Errorf("start is %s, want %s", events[0].Start, tc.want)
			}
		})
	}
}

func TestDecodeResource_FillsInAMissingEnd(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want provider.EventTime
	}{
		{
			name: "an all-day event lasts one day",
			body: "UID:feiertag\r\nDTSTART;VALUE=DATE:20260501\r\n",
			want: provider.Date(2026, time.May, 2),
		},
		{
			name: "a duration is added to the start",
			body: "UID:probe\r\nDTSTART;TZID=Europe/Berlin:20260301T180000\r\nDURATION:PT90M\r\n",
			want: provider.Zoned(time.Date(2026, time.March, 1, 19, 30, 0, 0, time.UTC), "Europe/Berlin"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			events, err := decodeResource("probe.ics", wrap(tc.body))
			if err != nil {
				t.Fatalf("decode resource: %v", err)
			}
			if !events[0].End.Equal(tc.want) {
				t.Errorf("end is %s, want %s", events[0].End, tc.want)
			}
		})
	}
}

func TestDecodeResource_RefusesAnObjectWithNoEvent(t *testing.T) {
	t.Parallel()

	_, err := decodeResource("leer.ics", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n")
	if !errors.Is(err, ErrServer) {
		t.Fatalf("got %v, want ErrServer", err)
	}
}

func TestDecodeResource_RefusesAnEventWithNoStart(t *testing.T) {
	t.Parallel()

	_, err := decodeResource("probe.ics", wrap("UID:probe\r\nSUMMARY:Probe\r\n"))
	if !errors.Is(err, ErrServer) {
		t.Fatalf("got %v, want ErrServer", err)
	}
}

func TestDecodeResource_Attendees(t *testing.T) {
	t.Parallel()

	body := "UID:abstimmung\r\n" +
		"DTSTART;TZID=Europe/Berlin:20260301T100000\r\n" +
		"DTEND;TZID=Europe/Berlin:20260301T110000\r\n" +
		"ORGANIZER;CN=Anna Beispiel:mailto:anna.beispiel@example.com\r\n" +
		"ATTENDEE;CN=Jonas Weber;PARTSTAT=ACCEPTED:mailto:jonas.weber@example.com\r\n" +
		"ATTENDEE;ROLE=OPT-PARTICIPANT;PARTSTAT=DECLINED:mailto:mira.koch@example.com\r\n"

	events, err := decodeResource("abstimmung.ics", wrap(body))
	if err != nil {
		t.Fatalf("decode resource: %v", err)
	}
	event := events[0]

	if len(event.Attendees) != 2 {
		t.Fatalf("got %d attendees, want two", len(event.Attendees))
	}
	if event.Attendees[0] != (provider.Attendee{
		Email: "jonas.weber@example.com", Name: "Jonas Weber", Response: provider.ResponseAccepted,
	}) {
		t.Errorf("first attendee is %+v", event.Attendees[0])
	}
	if !event.Attendees[1].Optional || event.Attendees[1].Response != provider.ResponseDeclined {
		t.Errorf("second attendee is %+v", event.Attendees[1])
	}
	if event.Organizer == nil || event.Organizer.Email != "anna.beispiel@example.com" {
		t.Errorf("organizer is %+v", event.Organizer)
	}
}

func TestEncodeResource_RoundTrip(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	event := provider.Event{
		UID:          "probe-20260301",
		Summary:      "Probe Hauptbuehne",
		Description:  "Aufbau ab 16:00\nSoundcheck 18:00",
		Location:     "Stadthalle, Saal 2",
		Start:        provider.Zoned(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:          provider.Zoned(time.Date(2026, time.March, 1, 22, 30, 0, 0, time.UTC), "Europe/Berlin"),
		Status:       provider.StatusTentative,
		Transparency: provider.TransparencyTransparent,
		Visibility:   provider.VisibilityPrivate,
		Recurrence:   provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY;BYDAY=SU"}},
		Marker:       provider.Marker{Edge: "edge-17", Source: "google-evt-991"},
		Created:      stamp,
	}

	object, err := encodeResource(event, stamp, provider.Capabilities{})
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}

	back, err := decodeResource("probe.ics", object)
	if err != nil {
		t.Fatalf("decode what was encoded: %v", err)
	}
	got := back[0]

	if got.UID != event.UID || got.Summary != event.Summary || got.Location != event.Location {
		t.Errorf("identity or text changed: %+v", got)
	}
	if got.Description != event.Description {
		t.Errorf("description is %q, want %q", got.Description, event.Description)
	}
	if !got.Start.Equal(event.Start) || !got.End.Equal(event.End) {
		t.Errorf("times changed: %s to %s", got.Start, got.End)
	}
	if got.Status != event.Status || got.Transparency != event.Transparency || got.Visibility != event.Visibility {
		t.Errorf("enums changed: %+v", got)
	}
	if got.Marker != event.Marker {
		t.Errorf("marker changed: %+v", got.Marker)
	}
	if strings.Join(got.Recurrence.Rule, "|") != strings.Join(event.Recurrence.Rule, "|") {
		t.Errorf("rule changed: %q", got.Recurrence.Rule)
	}
	if !hasVTimezone(object, "Europe/Berlin") {
		t.Error("a zoned event was written without the VTIMEZONE RFC 5545 requires")
	}
}

func TestEncodeResource_AllDayEvent(t *testing.T) {
	t.Parallel()

	event := provider.Event{
		UID:     "ferien",
		Summary: "Sommerferien",
		Start:   provider.Date(2026, time.July, 1),
		End:     provider.Date(2026, time.July, 15),
	}

	object, err := encodeResource(event, time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), provider.Capabilities{})
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}
	if !strings.Contains(object, "DTSTART;VALUE=DATE:20260701") {
		t.Errorf("an all-day start was not written as a date:\n%s", object)
	}
	if strings.Contains(object, "BEGIN:VTIMEZONE") {
		t.Error("an all-day event names no zone, so the object needs no VTIMEZONE")
	}
}

func TestEncodeResource_RefusesAnEventThatBreaksTheRules(t *testing.T) {
	t.Parallel()

	event := provider.Event{UID: "kaputt"}

	_, err := encodeResource(event, time.Now(), provider.Capabilities{})
	if !errors.Is(err, provider.ErrInvalidEvent) {
		t.Fatalf("got %v, want provider.Event.Validate to refuse it", err)
	}
}

func TestTimezoneName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "a calendar in a named zone",
			input: "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\n" +
				"END:VTIMEZONE\r\nEND:VCALENDAR\r\n",
			want: "Europe/Berlin",
		},
		{
			name: "a zone the database does not know",
			input: "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTIMEZONE\r\nTZID:Customized Time Zone\r\n" +
				"END:VTIMEZONE\r\nEND:VCALENDAR\r\n",
			want: "",
		},
		{name: "no property at all", input: "", want: ""},
		{name: "a property that is not an icalendar object", input: "Europe/Berlin", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := timezoneName(tc.input); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// hasVTimezone reports whether the object carries a VTIMEZONE for the
// zone.
func hasVTimezone(object, zone string) bool {
	return strings.Contains(object, "BEGIN:VTIMEZONE") && strings.Contains(object, "TZID:"+zone)
}
