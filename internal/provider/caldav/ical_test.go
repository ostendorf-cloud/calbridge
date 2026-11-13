// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestContentLines_Unfolding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "a line folded with a space",
			input: "SUMMARY:Probe auf der\r\n  Hauptbuehne\r\n",
			want:  "Probe auf der Hauptbuehne",
		},
		{
			name:  "a line folded with a tab",
			input: "SUMMARY:Probe auf der\r\n\t Hauptbuehne\r\n",
			want:  "Probe auf der Hauptbuehne",
		},
		{
			name:  "a stream with bare line feeds",
			input: "SUMMARY:Probe\n",
			want:  "Probe",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lines, err := contentLines(tc.input)
			if err != nil {
				t.Fatalf("read content lines: %v", err)
			}
			if len(lines) != 1 {
				t.Fatalf("got %d lines, want one", len(lines))
			}
			if lines[0].Value != tc.want {
				t.Errorf("value is %q, want %q", lines[0].Value, tc.want)
			}
		})
	}
}

func TestParseContentLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		wantName  string
		wantValue string
		wantParam map[string]string
	}{
		{
			name:      "a property with no parameter",
			input:     "SUMMARY:Standup",
			wantName:  "SUMMARY",
			wantValue: "Standup",
		},
		{
			name:      "a property with a zone parameter",
			input:     "DTSTART;TZID=Europe/Berlin:20260301T180000",
			wantName:  "DTSTART",
			wantValue: "20260301T180000",
			wantParam: map[string]string{"TZID": "Europe/Berlin"},
		},
		{
			name:      "a quoted parameter holding a colon",
			input:     `ATTENDEE;CN="Weber, Jonas":mailto:jonas.weber@example.com`,
			wantName:  "ATTENDEE",
			wantValue: "mailto:jonas.weber@example.com",
			wantParam: map[string]string{"CN": "Weber, Jonas"},
		},
		{
			name:      "an escaped text value",
			input:     `LOCATION:Stadthalle\, Saal 2\nEingang West`,
			wantName:  "LOCATION",
			wantValue: "Stadthalle, Saal 2\nEingang West",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			line, err := parseContentLine(tc.input)
			if err != nil {
				t.Fatalf("parse content line: %v", err)
			}
			if line.Name != tc.wantName {
				t.Errorf("name is %q, want %q", line.Name, tc.wantName)
			}
			if line.Value != tc.wantValue {
				t.Errorf("value is %q, want %q", line.Value, tc.wantValue)
			}
			for key, want := range tc.wantParam {
				if got := line.param(key); got != want {
					t.Errorf("parameter %s is %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestParseICalendar_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
	}{
		{name: "a component that never ends", input: "BEGIN:VCALENDAR\r\n"},
		{name: "an end without a begin", input: "END:VCALENDAR\r\n"},
		{name: "a mismatched end", input: "BEGIN:VCALENDAR\r\nEND:VEVENT\r\n"},
		{name: "a property outside a component", input: "SUMMARY:Standup\r\n"},
		{name: "a line with no value", input: "BEGIN:VCALENDAR\r\nSUMMARY\r\nEND:VCALENDAR\r\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseICalendar(tc.input)
			if !errors.Is(err, ErrServer) {
				t.Fatalf("got %v, want the parser to refuse it with ErrServer", err)
			}
		})
	}
}

func TestFold(t *testing.T) {
	t.Parallel()

	long := "DESCRIPTION:" + strings.Repeat("a", 200)
	folded := fold(long)

	for _, line := range strings.Split(strings.TrimSuffix(folded, "\r\n"), "\r\n") {
		if len(line) > maxLineOctets {
			t.Fatalf("a folded line is %d octets, want at most %d: %q", len(line), maxLineOctets, line)
		}
	}

	lines, err := contentLines(folded)
	if err != nil {
		t.Fatalf("read back the folded line: %v", err)
	}
	if len(lines) != 1 || lines[0].Value != strings.Repeat("a", 200) {
		t.Fatalf("unfolding did not return the original value")
	}
}

func TestFold_KeepsAMultiByteCharacterWhole(t *testing.T) {
	t.Parallel()

	value := strings.Repeat("ä", 80)
	folded := fold("SUMMARY:" + value)

	lines, err := contentLines(folded)
	if err != nil {
		t.Fatalf("read back the folded line: %v", err)
	}
	if lines[0].Value != value {
		t.Errorf("value came back as %q", lines[0].Value)
	}
}

func TestEscapeText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "a comma", input: "Stadthalle, Saal 2", want: `Stadthalle\, Saal 2`},
		{name: "a semicolon", input: "Aufbau; Abbau", want: `Aufbau\; Abbau`},
		{name: "a backslash", input: `C:\Technik`, want: `C:\\Technik`},
		{name: "a line break", input: "Aufbau\r\nAbbau", want: `Aufbau\nAbbau`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := escapeText(tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if back := unescapeText(got); back != strings.ReplaceAll(tc.input, "\r\n", "\n") {
				t.Errorf("unescaping gave %q", back)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "an hour and a half", input: "PT1H30M", want: 90 * time.Minute},
		{name: "a whole day", input: "P1D", want: 24 * time.Hour},
		{name: "a week", input: "P2W", want: 14 * 24 * time.Hour},
		{name: "days with a time", input: "P15DT5H0M20S", want: 15*24*time.Hour + 5*time.Hour + 20*time.Second},
		{name: "a negative duration", input: "-PT30M", want: -30 * time.Minute},
		{name: "a value with no P", input: "T1H", wantErr: true},
		{name: "a number with no unit", input: "PT1H30", wantErr: true},
		{name: "a unit with no number", input: "PTH", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDuration(tc.input)
			if tc.wantErr {
				if !errors.Is(err, ErrServer) {
					t.Fatalf("got %v, want the parser to refuse it", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("parse duration: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
