// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"errors"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

func TestVTimezone_ZoneWithDaylightSaving(t *testing.T) {
	t.Parallel()

	block, covered, err := vtimezone("Europe/Berlin", 2026)
	if err != nil {
		t.Fatalf("build vtimezone: %v", err)
	}
	if !covered.IsZero() {
		t.Errorf("a zone on a yearly rule is described without end, got a horizon at %s", covered)
	}

	for _, want := range []string{
		"BEGIN:VTIMEZONE",
		"TZID:Europe/Berlin",
		"BEGIN:DAYLIGHT",
		"TZOFFSETFROM:+0100",
		"TZOFFSETTO:+0200",
		"RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU",
		"BEGIN:STANDARD",
		"RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU",
		"END:VTIMEZONE",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the component carries no %q:\n%s", want, block)
		}
	}
}

func TestVTimezone_ZoneWithoutDaylightSaving(t *testing.T) {
	t.Parallel()

	block, covered, err := vtimezone("Asia/Tokyo", 2026)
	if err != nil {
		t.Fatalf("build vtimezone: %v", err)
	}
	if !covered.IsZero() {
		t.Errorf("a zone that never changes is described without end, got a horizon at %s", covered)
	}

	if strings.Contains(block, "BEGIN:DAYLIGHT") {
		t.Errorf("a zone that never changes needs no daylight observance:\n%s", block)
	}
	for _, want := range []string{"BEGIN:STANDARD", "TZOFFSETTO:+0900", "DTSTART:19700101T000000"} {
		if !strings.Contains(block, want) {
			t.Errorf("the component carries no %q:\n%s", want, block)
		}
	}
}

func TestVTimezone_ParsesBackAsOneComponent(t *testing.T) {
	t.Parallel()

	block, _, err := vtimezone("America/New_York", 2026)
	if err != nil {
		t.Fatalf("build vtimezone: %v", err)
	}

	root, err := parseICalendar(block)
	if err != nil {
		t.Fatalf("the component the generator wrote does not parse: %v", err)
	}
	if root.Name != "VTIMEZONE" {
		t.Fatalf("the component is a %s", root.Name)
	}
	if root.value("TZID") != "America/New_York" {
		t.Errorf("TZID is %q", root.value("TZID"))
	}
	if len(root.children("DAYLIGHT")) != 1 || len(root.children("STANDARD")) != 1 {
		t.Errorf("want one observance of each kind, got %d daylight and %d standard",
			len(root.children("DAYLIGHT")), len(root.children("STANDARD")))
	}
}

func TestVTimezone_RefusesAZoneTheDatabaseDoesNotKnow(t *testing.T) {
	t.Parallel()

	_, _, err := vtimezone("Mars/Olympus", 2026)
	if !errors.Is(err, provider.ErrInvalidEvent) {
		t.Fatalf("got %v, want ErrInvalidEvent", err)
	}
}

func TestVTimezone_ReportsAHorizonForAZoneOnNoYearlyRule(t *testing.T) {
	t.Parallel()

	// Africa/Casablanca moves its clock around Ramadan, so its onsets
	// fall on no yearly rule and the generator can only write them out.
	block, covered, err := vtimezone("Africa/Casablanca", 2026)
	if err != nil {
		t.Fatalf("build vtimezone: %v", err)
	}
	if covered.IsZero() {
		t.Fatalf("a zone written out transition by transition stops describing itself:\n%s", block)
	}
	if strings.Contains(block, "RRULE") {
		t.Errorf("the generator claimed a yearly rule it could not derive:\n%s", block)
	}
	if covered.Year() > 2026+windowYears {
		t.Errorf("the horizon at %s lies past the window the generator looked at", covered)
	}
}

func TestUTCOffset(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		seconds int
		want    string
	}{
		{name: "one hour ahead", seconds: 3600, want: "+0100"},
		{name: "five hours behind", seconds: -5 * 3600, want: "-0500"},
		{name: "a half hour zone", seconds: 5*3600 + 1800, want: "+0530"},
		{name: "no offset", seconds: 0, want: "+0000"},
		{name: "an offset with seconds", seconds: 3600 + 60 + 2, want: "+010102"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := utcOffset(tc.seconds); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
