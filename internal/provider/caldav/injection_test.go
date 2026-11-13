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

// A source calendar is not trusted input: its text reaches this package
// through provider.Event, and provider.Event.Validate checks none of it
// for a line break. A break that survived into a written property would
// end the content line and begin another, so the object would hold a
// property no code here wrote. The tests below take every field that
// reaches the writer and prove the refusal, and then prove the refusal
// matters by reading the written object back with this package's own
// parser.

// injectedAttendee is the line an injection would add. It names a
// mailbox, because an ATTENDEE against a server implementing RFC 6638 is
// an invitation sent to that address, and the iCloud profile declares it
// writes none.
const injectedAttendee = "ATTENDEE;ROLE=REQ-PARTICIPANT:mailto:victim@example.com"

// writableEvent returns an event that encodes cleanly, so a test changes
// one field and the refusal is attributable to that field.
func writableEvent() provider.Event {
	return provider.Event{
		UID:     "probe-20260301",
		Summary: "Probe Hauptbuehne",
		Start:   provider.Zoned(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:     provider.Zoned(time.Date(2026, time.March, 1, 22, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
}

func TestEncodeResource_RefusesALineBreakInEveryFieldThatReachesTheWriter(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		build func(provider.Event) provider.Event
		caps  provider.Capabilities
	}{
		{
			name: "the uid",
			build: func(e provider.Event) provider.Event {
				e.UID = "probe\r\n" + injectedAttendee

				return e
			},
		},
		{
			name: "a recurrence rule line",
			build: func(e provider.Event) provider.Event {
				e.Recurrence.Rule = []string{"RRULE:FREQ=DAILY;COUNT=2\r\nORGANIZER:mailto:attacker@example.com"}

				return e
			},
		},
		{
			name: "a recurrence entry that is not a recurrence property",
			build: func(e provider.Event) provider.Event {
				e.Recurrence.Rule = []string{injectedAttendee}

				return e
			},
		},
		{
			name: "an attendee address",
			build: func(e provider.Event) provider.Event {
				e.Attendees = []provider.Attendee{{Email: "jonas.weber@example.com\r\n" + injectedAttendee}}

				return e
			},
			caps: provider.Capabilities{Attendees: true},
		},
		{
			name: "an attendee display name",
			build: func(e provider.Event) provider.Event {
				e.Attendees = []provider.Attendee{{
					Email: "jonas.weber@example.com",
					Name:  "Jonas\r\n" + injectedAttendee,
				}}

				return e
			},
			caps: provider.Capabilities{Attendees: true},
		},
		{
			name: "a marker the engine set",
			build: func(e provider.Event) provider.Event {
				e.Marker = provider.Marker{Edge: "edge-17" + string(rune(0))}

				return e
			},
		},
		{
			name: "a time zone name",
			build: func(e provider.Event) provider.Event {
				e.Start = provider.Zoned(e.Start.Wall, "Europe/Berlin\r\n"+injectedAttendee)
				e.End = provider.Zoned(e.End.Wall, "Europe/Berlin\r\n"+injectedAttendee)

				return e
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := encodeResource(tc.build(writableEvent()), stamp, tc.caps)
			if err == nil {
				t.Fatal("the value was written instead of refused")
			}
			if !errors.Is(err, provider.ErrInvalidEvent) && !errors.Is(err, provider.ErrUnsupported) {
				t.Fatalf("got %v, want the writer's own check to refuse it", err)
			}
		})
	}
}

func TestEncodeResource_SummaryKeepsALineBreakAsEscapedText(t *testing.T) {
	t.Parallel()

	// A line break in a text value is not an injection: RFC 5545 section
	// 3.3.11 escapes it into two characters, and it never reaches the
	// stream as a break. Refusing it would drop what the source calendar
	// actually said, so it is kept.
	event := writableEvent()
	event.Description = "Aufbau ab 16:00\r\n" + injectedAttendee

	object, err := encodeResource(event, time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), provider.Capabilities{})
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}

	back, err := parseICalendar(object)
	if err != nil {
		t.Fatalf("parse what was written: %v", err)
	}
	vevent := back.children("VEVENT")[0]
	if _, found := vevent.prop("ATTENDEE"); found {
		t.Fatal("an escaped line break produced a real ATTENDEE property")
	}
	if !strings.Contains(vevent.value("DESCRIPTION"), injectedAttendee) {
		t.Error("the escaped text did not survive as text")
	}
}

func TestEncodeResource_AnInjectedUidWouldHaveBecomeARealProperty(t *testing.T) {
	t.Parallel()

	// The refusal above is only worth having if the value would otherwise
	// have become a property. This builds the object the old writer would
	// have produced and reads it back with this package's own parser, so
	// the test states what the check prevents rather than only that it
	// fires.
	injected := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:probe\r\n" +
		injectedAttendee + "\r\nDTSTART:20260301T180000Z\r\nDTEND:20260301T200000Z\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	root, err := parseICalendar(injected)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	line, found := root.children("VEVENT")[0].prop("ATTENDEE")
	if !found {
		t.Fatal("the proof case does not reproduce: no ATTENDEE was parsed")
	}
	if line.Value != "mailto:victim@example.com" {
		t.Errorf("the injected attendee reads as %q", line.Value)
	}
}

func TestICalWriter_RefusesANameThatIsNotAName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		given string
	}{
		{name: "a name holding a colon", given: "ATTENDEE:mailto:victim@example.com;X"},
		{name: "a name holding a semicolon", given: "SUMMARY;X-EVIL=1"},
		{name: "a name holding a space", given: "X CALBRIDGE EDGE"},
		{name: "an empty name", given: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var w icalWriter
			w.raw(tc.given, nil, "v")

			if !errors.Is(w.err, provider.ErrInvalidEvent) {
				t.Fatalf("got %v, want the writer to refuse the name", w.err)
			}
			if w.String() != "" {
				t.Errorf("the refused line reached the stream: %q", w.String())
			}
		})
	}
}

func TestICalWriter_AcceptsTheNamesTheConnectorWrites(t *testing.T) {
	t.Parallel()

	// The check must not refuse what this package actually writes,
	// X- properties and hyphenated names among them.
	for _, name := range []string{
		"BEGIN", "UID", "DTSTAMP", "DTSTART", "DTEND", "LAST-MODIFIED",
		"RRULE", "EXDATE", "ATTENDEE", "TZOFFSETFROM", markerEdgeProp, markerSourceProp,
	} {
		var w icalWriter
		w.raw(name, nil, "v")
		if w.err != nil {
			t.Errorf("the writer refused %q: %v", name, w.err)
		}
	}
}

func TestICalWriter_RefusesAControlCharacterInAParameter(t *testing.T) {
	t.Parallel()

	var w icalWriter
	w.raw("ATTENDEE", []string{"CN=Jonas\r\nX-EVIL=1"}, "mailto:jonas.weber@example.com")

	if !errors.Is(w.err, provider.ErrInvalidEvent) {
		t.Fatalf("got %v, want the writer to refuse the parameter", w.err)
	}
	if strings.Contains(w.String(), "X-EVIL") {
		t.Error("the refused line reached the stream")
	}
}

func TestICalWriter_WritesNothingAfterARefusal(t *testing.T) {
	t.Parallel()

	var w icalWriter
	w.begin("VEVENT")
	w.raw("UID", nil, "probe\r\n"+injectedAttendee)
	w.raw("SUMMARY", nil, "Probe")
	w.end("VEVENT")

	if w.err == nil {
		t.Fatal("the writer did not refuse")
	}
	if strings.Contains(w.String(), "SUMMARY") {
		t.Error("the writer kept building after a refusal")
	}
}

func TestRecurrenceLines_AcceptsWhatTheConnectorItselfReadBack(t *testing.T) {
	t.Parallel()

	// The entries a source read produced must survive a write, or a
	// mirrored recurring event cannot be copied at all.
	rule := []string{
		"RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"EXDATE;TZID=Europe/Berlin:20260406T090000",
		`RDATE;TZID="Europe/Berlin":20260413T090000`,
	}

	lines, err := recurrenceLines(rule)
	if err != nil {
		t.Fatalf("recurrence lines: %v", err)
	}
	if len(lines) != len(rule) {
		t.Fatalf("got %d lines, want %d", len(lines), len(rule))
	}
	if lines[1].param("TZID") != "Europe/Berlin" {
		t.Errorf("the zone parameter came back as %q", lines[1].param("TZID"))
	}
}

func TestEncodeResource_DescribesAZoneNamedOnlyByARecurrenceLine(t *testing.T) {
	t.Parallel()

	// The endpoints are in UTC, so only the EXDATE names Europe/Berlin.
	// RFC 5545 section 3.6.5 still wants the VTIMEZONE, and without it
	// the excluded occurrence is read against another offset.
	event := writableEvent()
	event.Start = provider.Zoned(event.Start.Wall, "UTC")
	event.End = provider.Zoned(event.End.Wall, "UTC")
	event.Recurrence.Rule = []string{
		"RRULE:FREQ=WEEKLY;COUNT=10",
		"EXDATE;TZID=Europe/Berlin:20260406T090000",
	}

	object, err := encodeResource(event, time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), provider.Capabilities{})
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}
	if !strings.Contains(object, "TZID:Europe/Berlin") {
		t.Errorf("the zone the EXDATE names has no VTIMEZONE:\n%s", object)
	}
}

func TestDecodeResource_ReadsAZoneTheDatabaseDoesNotKnowAsFloating(t *testing.T) {
	t.Parallel()

	// A Windows style zone name is what an Exchange export writes, and a
	// CalDAV collection stores whatever TZID it was handed. The object
	// describes the zone in its own VTIMEZONE, and this package still
	// may not report the name as a zone: internal/provider's second
	// normalisation rule makes a zoned time name an IANA zone, and a
	// name nothing can resolve would come back on a write as a TZID with
	// no VTIMEZONE behind it.
	object := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Microsoft Corporation//Outlook 16.0//EN\r\n" +
		"BEGIN:VTIMEZONE\r\nTZID:W. Europe Standard Time\r\n" +
		"BEGIN:STANDARD\r\nDTSTART:16011028T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\n" +
		"RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
		"BEGIN:VEVENT\r\nUID:exchange-4711\r\n" +
		"DTSTART;TZID=W. Europe Standard Time:20260302T090000\r\n" +
		"DTEND;TZID=W. Europe Standard Time:20260302T100000\r\n" +
		"SUMMARY:Projektrunde\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	events, err := decodeResource("exchange-4711.ics", object)
	if err != nil {
		t.Fatalf("decode resource: %v", err)
	}

	want := provider.Floating(time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC))
	if !events[0].Start.Equal(want) {
		t.Errorf("start is %s, want %s", events[0].Start, want)
	}
	if events[0].Start.Zone != "" {
		t.Errorf("the unresolvable name %q left this package as a zone", events[0].Start.Zone)
	}
}

func TestEncodeResource_RefusesAZoneItCannotDescribe(t *testing.T) {
	t.Parallel()

	// The object must never name a TZID it does not define. Every zone
	// the event carries goes through zonesOf, which refuses one the zone
	// database cannot describe rather than dropping it and letting
	// writeEventTime name it anyway.
	stamp := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		build func(provider.Event) provider.Event
	}{
		{
			name: "a windows zone name on the start",
			build: func(e provider.Event) provider.Event {
				e.Start = provider.Zoned(e.Start.Wall, "W. Europe Standard Time")
				e.End = provider.Zoned(e.End.Wall, "W. Europe Standard Time")

				return e
			},
		},
		{
			name: "a windows zone name on the end alone",
			build: func(e provider.Event) provider.Event {
				e.End = provider.Zoned(e.End.Wall, "W. Europe Standard Time")

				return e
			},
		},
		{
			name: "a zone name that would split into a second parameter",
			build: func(e provider.Event) provider.Event {
				e.Start = provider.Zoned(e.Start.Wall, "Europe/Berlin;X-EVIL=1")
				e.End = provider.Zoned(e.End.Wall, "Europe/Berlin;X-EVIL=1")

				return e
			},
		},
		{
			name: "a zone named only by an excluded occurrence",
			build: func(e provider.Event) provider.Event {
				e.Recurrence.Rule = []string{
					"RRULE:FREQ=WEEKLY;COUNT=4",
					"EXDATE;TZID=W. Europe Standard Time:20260406T090000",
				}

				return e
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			object, err := encodeResource(tc.build(writableEvent()), stamp, provider.Capabilities{})
			if !errors.Is(err, provider.ErrInvalidEvent) {
				t.Fatalf("got %v, want the zone check to refuse it", err)
			}
			if object != "" {
				t.Errorf("an object was written anyway:\n%s", object)
			}
		})
	}
}

func TestEncodeResource_DefinesEveryZoneItNames(t *testing.T) {
	t.Parallel()

	// The guarantee stated as a property rather than as a case: whatever
	// a written object names as a TZID, it also carries a VTIMEZONE for.
	event := writableEvent()
	event.Recurrence.Rule = []string{
		"RRULE:FREQ=WEEKLY;COUNT=4",
		"EXDATE;TZID=America/New_York:20260406T090000",
	}

	object, err := encodeResource(event, time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), provider.Capabilities{})
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}

	root, err := parseICalendar(object)
	if err != nil {
		t.Fatalf("parse what was written: %v", err)
	}
	defined := map[string]bool{}
	for _, block := range root.children("VTIMEZONE") {
		defined[block.value("TZID")] = true
	}

	named := 0
	for _, line := range append(root.children("VEVENT")[0].Props, icalLine{}) {
		tzid := line.param("TZID")
		if tzid == "" {
			continue
		}
		named++
		if !defined[tzid] {
			t.Errorf("the object names TZID %q and defines no VTIMEZONE for it:\n%s", tzid, object)
		}
	}
	if named < 3 {
		t.Errorf("the case named %d zones, want the two endpoints and the excluded occurrence", named)
	}
}

func TestEncodeResource_RefusesAnEventTheZoneCannotBeDescribedFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		rule    []string
		end     time.Time
		wantErr bool
	}{
		{
			name:    "repeating without end",
			rule:    []string{"RRULE:FREQ=WEEKLY"},
			wantErr: true,
		},
		{
			name:    "repeating until past the horizon",
			rule:    []string{"RRULE:FREQ=YEARLY;UNTIL=20900301T170000Z"},
			wantErr: true,
		},
		{
			name:    "repeating a counted number of times inside the horizon",
			rule:    []string{"RRULE:FREQ=WEEKLY;COUNT=4"},
			wantErr: false,
		},
		{
			name:    "a single event inside the horizon",
			rule:    nil,
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event := writableEvent()
			event.Start = provider.Zoned(event.Start.Wall, "Africa/Casablanca")
			event.End = provider.Zoned(event.End.Wall, "Africa/Casablanca")
			event.Recurrence.Rule = tc.rule

			_, err := encodeResource(event, time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), provider.Capabilities{})
			if tc.wantErr {
				if !errors.Is(err, provider.ErrUnsupported) {
					t.Fatalf("got %v, want the zone coverage check to refuse it", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("encode resource: %v", err)
			}
		})
	}
}
