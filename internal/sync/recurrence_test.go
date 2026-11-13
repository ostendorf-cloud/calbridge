// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"errors"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

func TestParseRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lines   []string
		wantErr error
	}{
		{
			name:  "a weekly rule on two days",
			lines: []string{"RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH;COUNT=8"},
		},
		{
			name:  "a daily rule with an end date",
			lines: []string{"RRULE:FREQ=DAILY;UNTIL=20260401T000000Z"},
		},
		{
			name:  "a rule with an excluded date",
			lines: []string{"RRULE:FREQ=WEEKLY", "EXDATE:20260311T090000"},
		},
		{
			name:    "no rule at all",
			lines:   []string{"EXDATE:20260311T090000"},
			wantErr: ErrUnsupportedRecurrence,
		},
		{
			name:    "two rules",
			lines:   []string{"RRULE:FREQ=DAILY", "RRULE:FREQ=WEEKLY"},
			wantErr: ErrUnsupportedRecurrence,
		},
		{
			name:    "a monthly day of the week",
			lines:   []string{"RRULE:FREQ=MONTHLY;BYDAY=2TH"},
			wantErr: ErrUnsupportedRecurrence,
		},
		{
			name:    "a part the expander does not implement",
			lines:   []string{"RRULE:FREQ=MONTHLY;BYSETPOS=-1"},
			wantErr: ErrUnsupportedRecurrence,
		},
		{
			name:    "an added date",
			lines:   []string{"RRULE:FREQ=WEEKLY", "RDATE:20260311T090000"},
			wantErr: ErrUnsupportedRecurrence,
		},
		{
			name:    "a week starting on Sunday",
			lines:   []string{"RRULE:FREQ=WEEKLY;INTERVAL=2;WKST=SU"},
			wantErr: ErrUnsupportedRecurrence,
		},
		{
			name:    "an interval of zero",
			lines:   []string{"RRULE:FREQ=DAILY;INTERVAL=0"},
			wantErr: ErrUnsupportedRecurrence,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRule(tt.lines)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRule_occurrences(t *testing.T) {
	t.Parallel()

	start := provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin")
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		lines []string
		start provider.EventTime
		from  time.Time
		to    time.Time
		want  []string
	}{
		{
			name:  "a daily rule counted three times",
			lines: []string{"RRULE:FREQ=DAILY;COUNT=3"},
			want:  []string{"20260304T090000", "20260305T090000", "20260306T090000"},
		},
		{
			name:  "every second day",
			lines: []string{"RRULE:FREQ=DAILY;INTERVAL=2;COUNT=3"},
			want:  []string{"20260304T090000", "20260306T090000", "20260308T090000"},
		},
		{
			name:  "a weekly rule up to a date",
			lines: []string{"RRULE:FREQ=WEEKLY;UNTIL=20260325T090000Z"},
			want:  []string{"20260304T090000", "20260311T090000", "20260318T090000", "20260325T090000"},
		},
		{
			name:  "a weekly rule on two days",
			lines: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4"},
			want:  []string{"20260304T090000", "20260309T090000", "20260311T090000", "20260316T090000"},
		},
		{
			name:  "an excluded date drops one occurrence",
			lines: []string{"RRULE:FREQ=WEEKLY;COUNT=3", "EXDATE:20260311T090000"},
			want:  []string{"20260304T090000", "20260318T090000"},
		},
		{
			name:  "a monthly rule skips the months without that day",
			lines: []string{"RRULE:FREQ=MONTHLY;COUNT=4"},
			start: provider.Zoned(time.Date(2026, time.January, 31, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
			want:  []string{"20260131T090000", "20260331T090000", "20260531T090000", "20260731T090000"},
		},
		{
			name:  "a yearly rule",
			lines: []string{"RRULE:FREQ=YEARLY;COUNT=2"},
			to:    time.Date(2028, time.January, 1, 0, 0, 0, 0, time.UTC),
			want:  []string{"20260304T090000", "20270304T090000"},
		},
		{
			name:  "the window cuts the series short",
			lines: []string{"RRULE:FREQ=WEEKLY"},
			to:    time.Date(2026, time.March, 20, 0, 0, 0, 0, time.UTC),
			want:  []string{"20260304T090000", "20260311T090000", "20260318T090000"},
		},
		{
			name:  "the window hides the occurrences before it",
			lines: []string{"RRULE:FREQ=WEEKLY;COUNT=4"},
			from:  time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC),
			want:  []string{"20260318T090000", "20260325T090000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			at := tt.start
			if at.IsZero() {
				at = start
			}
			begin, end := tt.from, tt.to
			if begin.IsZero() {
				begin = from
			}
			if end.IsZero() {
				end = to
			}

			parsed, err := parseRule(tt.lines)
			if err != nil {
				t.Fatalf("parse rule: %v", err)
			}

			got, err := parsed.occurrences(at, begin, end)
			if err != nil {
				t.Fatalf("expand: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("occurrences: got %v, want %v", stamps(got), tt.want)
			}
			for i, occurrence := range got {
				if stampKey(occurrence.Wall) != tt.want[i] {
					t.Fatalf("occurrence %d: got %s, want %s (all: %v)", i, stampKey(occurrence.Wall), tt.want[i], stamps(got))
				}
				if occurrence.Kind != at.Kind || occurrence.Zone != at.Zone {
					t.Fatalf("occurrence %d keeps %s in %q, want %s in %q", i, occurrence.Kind, occurrence.Zone, at.Kind, at.Zone)
				}
			}
		})
	}
}

func TestRule_occurrences_refusesASeriesLargerThanTheLimit(t *testing.T) {
	t.Parallel()

	parsed, err := parseRule([]string{"RRULE:FREQ=DAILY"})
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}

	start := provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin")
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(20, 0, 0)

	if _, err := parsed.occurrences(start, from, to); !errors.Is(err, ErrRecurrenceTooLarge) {
		t.Fatalf("got %v, want %v", err, ErrRecurrenceTooLarge)
	}
}

// stamps renders occurrences for a failure message.
func stamps(times []provider.EventTime) []string {
	out := make([]string, 0, len(times))
	for _, t := range times {
		out = append(out, stampKey(t.Wall))
	}

	return out
}
