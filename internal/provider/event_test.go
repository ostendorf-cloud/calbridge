// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// wall is a clock reading with no zone, the way a provider hands one over
// before the connector decides what it is.
func wall(hour, minute int) time.Time {
	return time.Date(2026, time.March, 29, hour, minute, 0, 0, time.UTC)
}

func TestEventTimeValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		time  provider.EventTime
		valid bool
	}{
		{
			name:  "an all-day date",
			time:  provider.Date(2026, time.March, 29),
			valid: true,
		},
		{
			name:  "a wall time in a named zone",
			time:  provider.Zoned(wall(9, 30), "Europe/Berlin"),
			valid: true,
		},
		{
			name:  "an instant as a zoned time in UTC",
			time:  provider.Zoned(wall(9, 30), "UTC"),
			valid: true,
		},
		{
			name:  "a floating wall time",
			time:  provider.Floating(wall(9, 30)),
			valid: true,
		},
		{
			name:  "an endpoint a connector never mapped",
			time:  provider.EventTime{},
			valid: false,
		},
		{
			name:  "a date carrying a time of day",
			time:  provider.EventTime{Kind: provider.TimeKindDate, Wall: wall(9, 30)},
			valid: false,
		},
		{
			name: "a date carrying a zone",
			time: provider.EventTime{
				Kind: provider.TimeKindDate,
				Wall: wall(0, 0),
				Zone: "Europe/Berlin",
			},
			valid: false,
		},
		{
			name:  "a zoned time naming no zone",
			time:  provider.EventTime{Kind: provider.TimeKindZoned, Wall: wall(9, 30)},
			valid: false,
		},
		{
			name: "a floating time carrying a zone",
			time: provider.EventTime{
				Kind: provider.TimeKindFloating,
				Wall: wall(9, 30),
				Zone: "Europe/Berlin",
			},
			valid: false,
		},
		{
			name:  "a zoned time carrying no date",
			time:  provider.EventTime{Kind: provider.TimeKindZoned, Zone: "Europe/Berlin"},
			valid: false,
		},
		{
			name:  "a kind outside the three",
			time:  provider.EventTime{Kind: provider.TimeKind(9), Wall: wall(9, 30)},
			valid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.time.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("Validate() error = %v, want nil", err)
			case !tc.valid && !errors.Is(err, provider.ErrInvalidEvent):
				t.Errorf("Validate() error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestEventTimeConstructors(t *testing.T) {
	t.Parallel()

	t.Run("a date carries no time of day", func(t *testing.T) {
		t.Parallel()

		got := provider.Date(2026, time.March, 29)
		if h, m, s := got.Wall.Clock(); h != 0 || m != 0 || s != 0 {
			t.Errorf("Date() clock = %02d:%02d:%02d, want midnight", h, m, s)
		}
		if got.Zone != "" {
			t.Errorf("Date() zone = %q, want empty", got.Zone)
		}
	})

	t.Run("a zoned time keeps the clock reading and not the location", func(t *testing.T) {
		t.Parallel()

		berlin, err := time.LoadLocation("Europe/Berlin")
		if err != nil {
			t.Skipf("zone database not available: %v", err)
		}

		got := provider.Zoned(time.Date(2026, time.March, 29, 9, 30, 0, 0, berlin), "Europe/Berlin")
		if h, m := got.Wall.Hour(), got.Wall.Minute(); h != 9 || m != 30 {
			t.Errorf("Zoned() clock = %02d:%02d, want 09:30", h, m)
		}
		if got.Wall.Location() != time.UTC {
			t.Errorf("Zoned() location = %v, want UTC, which carries no meaning", got.Wall.Location())
		}
		if got.Zone != "Europe/Berlin" {
			t.Errorf("Zoned() zone = %q, want Europe/Berlin", got.Zone)
		}
	})

	t.Run("a floating time names no zone", func(t *testing.T) {
		t.Parallel()

		if got := provider.Floating(wall(9, 30)); got.Zone != "" {
			t.Errorf("Floating() zone = %q, want empty", got.Zone)
		}
	})

	t.Run("the zero endpoint reports itself as unset", func(t *testing.T) {
		t.Parallel()

		if !(provider.EventTime{}).IsZero() {
			t.Error("EventTime{}.IsZero() = false, want true")
		}
		if provider.Date(2026, time.March, 29).IsZero() {
			t.Error("Date().IsZero() = true, want false")
		}
	})
}

func TestEventTimeEqual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		left  provider.EventTime
		right provider.EventTime
		want  bool
	}{
		{
			name:  "the same reading in the same zone",
			left:  provider.Zoned(wall(9, 30), "Europe/Berlin"),
			right: provider.Zoned(wall(9, 30), "Europe/Berlin"),
			want:  true,
		},
		{
			name:  "the same reading in different zones",
			left:  provider.Zoned(wall(9, 30), "Europe/Berlin"),
			right: provider.Zoned(wall(9, 30), "UTC"),
			want:  false,
		},
		{
			name:  "the same reading floating and zoned",
			left:  provider.Floating(wall(9, 30)),
			right: provider.Zoned(wall(9, 30), "UTC"),
			want:  false,
		},
		{
			name:  "different readings",
			left:  provider.Zoned(wall(9, 30), "UTC"),
			right: provider.Zoned(wall(10, 30), "UTC"),
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.left.Equal(tc.right); got != tc.want {
				t.Errorf("Equal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEventTimeBefore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		left       provider.EventTime
		right      provider.EventTime
		wantBefore bool
		wantKnown  bool
	}{
		{
			name:       "two readings in the same zone",
			left:       provider.Zoned(wall(9, 30), "Europe/Berlin"),
			right:      provider.Zoned(wall(10, 30), "Europe/Berlin"),
			wantBefore: true,
			wantKnown:  true,
		},
		{
			name:       "two dates",
			left:       provider.Date(2026, time.March, 29),
			right:      provider.Date(2026, time.March, 30),
			wantBefore: true,
			wantKnown:  true,
		},
		{
			name:       "two floating readings",
			left:       provider.Floating(wall(10, 30)),
			right:      provider.Floating(wall(9, 30)),
			wantBefore: false,
			wantKnown:  true,
		},
		{
			name:      "two readings in different zones",
			left:      provider.Zoned(wall(9, 30), "Europe/Berlin"),
			right:     provider.Zoned(wall(10, 30), "America/New_York"),
			wantKnown: false,
		},
		{
			name:      "a date against a zoned reading",
			left:      provider.Date(2026, time.March, 29),
			right:     provider.Zoned(wall(10, 30), "Europe/Berlin"),
			wantKnown: false,
		},
		{
			name:      "an endpoint that was never mapped",
			left:      provider.EventTime{},
			right:     provider.EventTime{},
			wantKnown: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before, known := tc.left.Before(tc.right)
			if known != tc.wantKnown {
				t.Fatalf("Before() known = %v, want %v", known, tc.wantKnown)
			}
			if known && before != tc.wantBefore {
				t.Errorf("Before() = %v, want %v", before, tc.wantBefore)
			}
		})
	}
}

func TestEventTimeString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		time provider.EventTime
		want string
	}{
		{name: "a date", time: provider.Date(2026, time.March, 29), want: "2026-03-29"},
		{
			name: "a zoned reading",
			time: provider.Zoned(wall(9, 30), "Europe/Berlin"),
			want: "2026-03-29T09:30:00 Europe/Berlin",
		},
		{
			name: "a floating reading",
			time: provider.Floating(wall(9, 30)),
			want: "2026-03-29T09:30:00 floating",
		},
		{name: "an unset endpoint", time: provider.EventTime{}, want: "unset"},
		{
			name: "a kind outside the three",
			time: provider.EventTime{Kind: provider.TimeKind(9), Wall: wall(9, 30)},
			want: "TimeKind(9)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.time.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// meeting returns a valid timed event, the starting point each validation
// case changes one thing about.
func meeting() provider.Event {
	return provider.Event{
		ID:      "evt-1",
		UID:     "6f4d2d8a-6e3a-4f1e-9a7c-1f2c4d5e6a7b",
		Summary: "Sprint review",
		Start:   provider.Zoned(wall(9, 30), "Europe/Berlin"),
		End:     provider.Zoned(wall(10, 30), "Europe/Berlin"),
	}
}

func TestEventValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		event provider.Event
		valid bool
	}{
		{name: "a timed event", event: meeting(), valid: true},
		{
			name: "an all-day event ending the next day",
			event: provider.Event{
				Summary: "Public holiday",
				Start:   provider.Date(2026, time.March, 29),
				End:     provider.Date(2026, time.March, 30),
			},
			valid: true,
		},
		{
			name: "a busy block with no title",
			event: provider.Event{
				Start:        provider.Zoned(wall(9, 30), "Europe/Berlin"),
				End:          provider.Zoned(wall(10, 30), "Europe/Berlin"),
				Transparency: provider.TransparencyOpaque,
				Visibility:   provider.VisibilityPrivate,
			},
			valid: true,
		},
		{
			name: "an event crossing a zone boundary",
			event: provider.Event{
				Summary: "Flight to Boston",
				Start:   provider.Zoned(wall(9, 30), "Europe/Berlin"),
				End:     provider.Zoned(wall(11, 45), "America/New_York"),
			},
			valid: true,
		},
		{
			name: "a repeating master",
			event: provider.Event{
				Summary:    "Weekly sync",
				Start:      provider.Zoned(wall(9, 30), "Europe/Berlin"),
				End:        provider.Zoned(wall(10, 30), "Europe/Berlin"),
				Recurrence: provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO"}},
			},
			valid: true,
		},
		{
			name: "one instance of a master",
			event: provider.Event{
				Summary: "Weekly sync",
				Start:   provider.Zoned(wall(11, 0), "Europe/Berlin"),
				End:     provider.Zoned(wall(12, 0), "Europe/Berlin"),
				Recurrence: provider.Recurrence{
					MasterID:      "evt-1",
					OriginalStart: provider.Zoned(wall(9, 30), "Europe/Berlin"),
				},
			},
			valid: true,
		},
		{
			name: "an event with no start",
			event: provider.Event{
				Summary: "Sprint review",
				End:     provider.Zoned(wall(10, 30), "Europe/Berlin"),
			},
			valid: false,
		},
		{
			name: "an event with no end",
			event: provider.Event{
				Summary: "Sprint review",
				Start:   provider.Zoned(wall(9, 30), "Europe/Berlin"),
			},
			valid: false,
		},
		{
			name: "an all-day start with a timed end",
			event: provider.Event{
				Summary: "Sprint review",
				Start:   provider.Date(2026, time.March, 29),
				End:     provider.Zoned(wall(10, 30), "Europe/Berlin"),
			},
			valid: false,
		},
		{
			name: "an end before the start",
			event: provider.Event{
				Summary: "Sprint review",
				Start:   provider.Zoned(wall(10, 30), "Europe/Berlin"),
				End:     provider.Zoned(wall(9, 30), "Europe/Berlin"),
			},
			valid: false,
		},
		{
			name: "an all-day event ending on its start date",
			event: provider.Event{
				Summary: "Public holiday",
				Start:   provider.Date(2026, time.March, 29),
				End:     provider.Date(2026, time.March, 29),
			},
			valid: false,
		},
		{
			name: "a status outside the three",
			event: func() provider.Event {
				e := meeting()
				e.Status = provider.Status(9)

				return e
			}(),
			valid: false,
		},
		{
			name: "a visibility outside the four",
			event: func() provider.Event {
				e := meeting()
				e.Visibility = provider.Visibility(9)

				return e
			}(),
			valid: false,
		},
		{
			name: "a transparency outside the two",
			event: func() provider.Event {
				e := meeting()
				e.Transparency = provider.Transparency(9)

				return e
			}(),
			valid: false,
		},
		{
			name: "an attendee with no address",
			event: func() provider.Event {
				e := meeting()
				e.Attendees = []provider.Attendee{{Name: "Simon Ostendorf"}}

				return e
			}(),
			valid: false,
		},
		{
			name: "an attendee response outside the four",
			event: func() provider.Event {
				e := meeting()
				e.Attendees = []provider.Attendee{{Email: "simon@example.com", Response: provider.Response(9)}}

				return e
			}(),
			valid: false,
		},
		{
			name: "an event that is both a master and an instance",
			event: func() provider.Event {
				e := meeting()
				e.Recurrence = provider.Recurrence{
					Rule:          []string{"RRULE:FREQ=WEEKLY"},
					MasterID:      "evt-2",
					OriginalStart: provider.Zoned(wall(9, 30), "Europe/Berlin"),
				}

				return e
			}(),
			valid: false,
		},
		{
			name: "an instance with no original start",
			event: func() provider.Event {
				e := meeting()
				e.Recurrence = provider.Recurrence{MasterID: "evt-2"}

				return e
			}(),
			valid: false,
		},
		{
			name: "an instance whose original start breaks the rules",
			event: func() provider.Event {
				e := meeting()
				e.Recurrence = provider.Recurrence{
					MasterID:      "evt-2",
					OriginalStart: provider.EventTime{Kind: provider.TimeKindZoned, Wall: wall(9, 30)},
				}

				return e
			}(),
			valid: false,
		},
		{
			name: "an original start on an event that is not an instance",
			event: func() provider.Event {
				e := meeting()
				e.Recurrence = provider.Recurrence{OriginalStart: provider.Zoned(wall(9, 30), "Europe/Berlin")}

				return e
			}(),
			valid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.event.Validate()
			switch {
			case tc.valid && err != nil:
				t.Errorf("Validate() error = %v, want nil", err)
			case !tc.valid && !errors.Is(err, provider.ErrInvalidEvent):
				t.Errorf("Validate() error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestRecurrenceShape(t *testing.T) {
	t.Parallel()

	t.Run("a master carries rules and no master id", func(t *testing.T) {
		t.Parallel()

		r := provider.Recurrence{Rule: []string{"RRULE:FREQ=DAILY;COUNT=5"}}
		if !r.IsMaster() {
			t.Error("IsMaster() = false, want true")
		}
		if r.IsInstance() {
			t.Error("IsInstance() = true, want false")
		}
	})

	t.Run("an instance names its master and no rules", func(t *testing.T) {
		t.Parallel()

		r := provider.Recurrence{
			MasterID:      "evt-1",
			OriginalStart: provider.Date(2026, time.March, 29),
		}
		if r.IsMaster() {
			t.Error("IsMaster() = true, want false")
		}
		if !r.IsInstance() {
			t.Error("IsInstance() = false, want true")
		}
	})

	t.Run("a single event is neither", func(t *testing.T) {
		t.Parallel()

		var r provider.Recurrence
		if r.IsMaster() || r.IsInstance() {
			t.Error("the zero Recurrence reports itself as a master or an instance")
		}
	})
}

func TestMarkerIsZero(t *testing.T) {
	t.Parallel()

	if !(provider.Marker{}).IsZero() {
		t.Error("Marker{}.IsZero() = false, want true")
	}
	if (provider.Marker{Edge: "edge-1", Source: "evt-9"}).IsZero() {
		t.Error("a filled Marker reports itself as zero")
	}
}
