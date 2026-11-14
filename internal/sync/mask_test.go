// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

func TestMaskFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		level   storage.DetailLevel
		want    string
		wantErr error
	}{
		{
			name:  "full copy",
			level: storage.DetailLevelFull,
			want:  "summary,description,location,attendees,status,transparency,visibility,recurrence",
		},
		{
			// The visibility travels with the title: a copy that keeps a
			// private event's title and drops its restriction shows it to
			// everyone the target calendar is shared with.
			name:  "title and time",
			level: storage.DetailLevelTitleTime,
			want:  "summary,status,transparency,visibility,recurrence",
		},
		{
			name:  "busy block",
			level: storage.DetailLevelBusyBlock,
			want:  "status,transparency",
		},
		{
			name:    "a level nobody defined",
			level:   storage.DetailLevel("everything"),
			wantErr: ErrInvalidDetailLevel,
		},
		{
			name:    "no level at all",
			level:   "",
			wantErr: ErrInvalidDetailLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mask, err := MaskFor(tt.level)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if mask.String() != tt.want {
				t.Fatalf("fields: got %q, want %q", mask, tt.want)
			}
		})
	}
}

func TestFieldMask_WithAndWithout(t *testing.T) {
	t.Parallel()

	base := maskBusyBlock

	widened := base.With(FieldLocation, FieldSummary)
	if !widened.Has(FieldLocation) || !widened.Has(FieldSummary) || !widened.Has(FieldTransparency) {
		t.Fatalf("widened mask: got %q", widened)
	}

	narrowed := widened.Without(FieldSummary)
	if narrowed.Has(FieldSummary) {
		t.Fatalf("narrowed mask still copies the summary: %q", narrowed)
	}

	if base.Has(FieldLocation) {
		t.Fatalf("the preset itself changed: %q", base)
	}
}

func TestFieldMask_Within(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mask  FieldMask
		bound FieldMask
		want  bool
	}{
		{name: "a busy block inside a full copy", mask: maskBusyBlock, bound: maskFull, want: true},
		{name: "a full copy inside a busy block", mask: maskFull, bound: maskBusyBlock},
		{name: "a preset inside itself", mask: maskTitleTime, bound: maskTitleTime, want: true},
		{name: "one overridden field outside the bound", mask: maskBusyBlock.With(FieldLocation), bound: maskTitleTime},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mask.Within(tt.bound); got != tt.want {
				t.Fatalf("%q within %q: got %t, want %t", tt.mask, tt.bound, got, tt.want)
			}
		})
	}
}

func TestFieldMask_project(t *testing.T) {
	t.Parallel()

	source := provider.Event{
		ID:          "evt-1",
		UID:         "uid-1",
		ETag:        "etag-1",
		Summary:     "Jour fixe",
		Description: "Agenda im Anhang",
		Start:       provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:         provider.Zoned(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Recurrence:  provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY"}},
		Attendees:   []provider.Attendee{{Email: "lena@example.com"}},
		Created:     time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
	}
	marker := provider.Marker{Edge: "edge-1", Source: "evt-1"}

	target := Endpoint{
		Connector: stubConnector{caps: provider.Capabilities{NativeRecurrence: true, Attendees: true}},
		TimeZone:  "Europe/Berlin",
	}

	copied, err := maskFull.project(source, target, marker)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	if copied.ID != "" || copied.UID != "" || copied.ETag != "" || !copied.Created.IsZero() {
		t.Fatalf("the projection carries the provider's own fields: %+v", copied)
	}
	if copied.Marker != marker {
		t.Fatalf("marker: got %+v, want %+v", copied.Marker, marker)
	}
	if len(copied.Recurrence.Rule) != 1 || len(copied.Attendees) != 1 {
		t.Fatalf("rule and attendees: got %+v", copied)
	}

	// The same source through a target that holds neither keeps nothing
	// the connector would have dropped anyway.
	plain := Endpoint{Connector: stubConnector{}, TimeZone: "Europe/Berlin"}

	reduced, err := maskFull.project(source, plain, marker)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(reduced.Recurrence.Rule) != 0 || len(reduced.Attendees) != 0 {
		t.Fatalf("the projection kept what the target cannot store: %+v", reduced)
	}
}

func TestFieldMask_project_copiesNothingTheMaskDoesNotName(t *testing.T) {
	t.Parallel()

	// project is an allow-list: it names each field it copies, so a field
	// added to provider.Event later is dropped until somebody says
	// otherwise. This pins that. A source event with every field set,
	// copied at the narrowest preset, must differ from the empty event in
	// exactly the five things a busy block is.
	loud := provider.Event{
		ID:           "evt-1",
		UID:          "uid-1",
		ETag:         "etag-1",
		Summary:      "Zahnarzt",
		Description:  "Kontrolle, bitte Karte mitbringen",
		Location:     "Hauptstrasse 3",
		Start:        provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:          provider.Zoned(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Status:       provider.StatusTentative,
		Transparency: provider.TransparencyTransparent,
		Visibility:   provider.VisibilityConfidential,
		Recurrence:   provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY"}},
		Attendees:    []provider.Attendee{{Email: "praxis@example.com", Name: "Praxis Wagner"}},
		Organizer:    &provider.Attendee{Email: "praxis@example.com"},
		Marker:       provider.Marker{Edge: "edge-9", Source: "evt-9"},
		Created:      time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		Updated:      time.Date(2026, time.February, 2, 0, 0, 0, 0, time.UTC),
	}
	marker := provider.Marker{Edge: "edge-1", Source: "evt-1"}

	target := Endpoint{
		Connector: stubConnector{caps: provider.Capabilities{NativeRecurrence: true, Attendees: true}},
		TimeZone:  "Europe/Berlin",
	}

	got, err := maskBusyBlock.project(loud, target, marker)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	want := provider.Event{
		Start:        loud.Start,
		End:          loud.End,
		Status:       loud.Status,
		Transparency: loud.Transparency,
		Marker:       marker,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the busy block copy carries more than start, end, status, transparency and the marker:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestAnchor(t *testing.T) {
	t.Parallel()

	floating := provider.Floating(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC))

	tests := []struct {
		name    string
		time    provider.EventTime
		caps    provider.Capabilities
		zone    string
		want    provider.EventTime
		wantErr error
	}{
		{
			name: "a target that stores floating times keeps it floating",
			time: floating,
			caps: provider.Capabilities{FloatingTimes: true},
			zone: "Europe/Berlin",
			want: floating,
		},
		{
			name: "a target that does not anchors it in the calendar's zone",
			time: floating,
			zone: "Europe/Berlin",
			want: provider.Zoned(floating.Wall, "Europe/Berlin"),
		},
		{
			name:    "a calendar with no zone leaves nothing to anchor in",
			time:    floating,
			wantErr: ErrNoTargetZone,
		},
		{
			name: "a zoned time is untouched",
			time: provider.Zoned(floating.Wall, "Europe/Berlin"),
			want: provider.Zoned(floating.Wall, "Europe/Berlin"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := anchor(tt.time, tt.caps, tt.zone)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if !got.Equal(tt.want) {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
