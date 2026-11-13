// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// hashed is the event every hash case starts from.
func hashed() provider.Event {
	return provider.Event{
		ID:      "evt-1",
		Summary: "Jour fixe",
		Start:   provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:     provider.Zoned(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Marker:  provider.Marker{Edge: "edge-1", Source: "evt-1"},
	}
}

func TestContentHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		change   func(*provider.Event)
		mask     FieldMask
		wantSame bool
	}{
		{
			name:     "the same event hashes the same",
			change:   func(*provider.Event) {},
			wantSame: true,
		},
		{
			name:     "the provider's own fields do not count",
			change:   func(e *provider.Event) { e.ID = "evt-9"; e.ETag = "etag-9"; e.Updated = time.Now() },
			wantSame: true,
		},
		{
			name:   "a changed title counts",
			change: func(e *provider.Event) { e.Summary = "Jour fixe, verlegt" },
		},
		{
			name:   "a changed time counts",
			change: func(e *provider.Event) { e.Start = provider.Zoned(e.Start.Wall.Add(time.Hour), "Europe/Berlin") },
		},
		{
			name:   "the same reading in another zone counts",
			change: func(e *provider.Event) { e.Start = provider.Zoned(e.Start.Wall, "America/New_York") },
		},
		{
			name:   "a changed marker counts",
			change: func(e *provider.Event) { e.Marker.Source = "evt-other" },
		},
		{
			name:   "an added attendee counts",
			change: func(e *provider.Event) { e.Attendees = []provider.Attendee{{Email: "lena@example.com"}} },
		},
		{
			name:   "a different mask counts",
			change: func(*provider.Event) {},
			mask:   maskBusyBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := contentHash(hashed(), maskFull)

			event := hashed()
			tt.change(&event)
			mask := maskFull
			if tt.mask != (FieldMask{}) {
				mask = tt.mask
			}

			got := contentHash(event, mask)
			if (got == base) != tt.wantSame {
				t.Fatalf("hash equal: got %t, want %t", got == base, tt.wantSame)
			}
		})
	}
}

func TestDetails(t *testing.T) {
	t.Parallel()

	event := hashed()
	event.Description = "Agenda im Anhang"
	event.Location = "Raum 2.14"

	cached, err := details(event, maskFull)
	if err != nil {
		t.Fatalf("details: %v", err)
	}

	var held cachedDetails
	if err := json.Unmarshal(cached, &held); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if held.Summary != event.Summary || held.Description != event.Description || held.Location != event.Location {
		t.Fatalf("cached details: got %+v", held)
	}

	// A busy block copies no content, so there is nothing to cache and
	// the column stays null.
	empty, err := details(event, maskBusyBlock)
	if err != nil {
		t.Fatalf("details: %v", err)
	}
	if empty != nil {
		t.Fatalf("busy block cached %q, want nothing", empty)
	}
}

func TestInstant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		time provider.EventTime
		want time.Time
	}{
		{
			name: "a zoned time resolves in its own zone",
			time: provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
			want: time.Date(2026, time.March, 4, 8, 0, 0, 0, time.UTC),
		},
		{
			name: "a date is the day itself",
			time: provider.Date(2026, time.March, 4),
			want: time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "a floating time is the reading itself",
			time: provider.Floating(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC)),
			want: time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := instant(tt.time)
			if err != nil {
				t.Fatalf("instant: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestInstant_reportsAZoneTheDatabaseDoesNotKnow(t *testing.T) {
	t.Parallel()

	at := provider.EventTime{
		Kind: provider.TimeKindZoned,
		Wall: time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC),
		Zone: "Mars/Olympus_Mons",
	}

	if _, err := instant(at); err == nil {
		t.Fatal("got no error for a zone that does not exist")
	}
}

func TestIsOwnWrite(t *testing.T) {
	t.Parallel()

	edgeID := uuid.New()
	other := uuid.New()

	tests := []struct {
		name         string
		marker       provider.Marker
		wantOwn      bool
		wantThisEdge bool
	}{
		{name: "an event nobody copied", marker: provider.Marker{}},
		{
			name:         "a copy this edge wrote",
			marker:       provider.Marker{Edge: edgeID.String(), Source: "evt-1"},
			wantOwn:      true,
			wantThisEdge: true,
		},
		{
			name:    "a copy another edge wrote",
			marker:  provider.Marker{Edge: other.String(), Source: "evt-1"},
			wantOwn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := provider.Event{ID: "evt-1", Marker: tt.marker}
			if got := IsOwnWrite(event); got != tt.wantOwn {
				t.Errorf("IsOwnWrite: got %t, want %t", got, tt.wantOwn)
			}
			if got := WrittenBy(event, edgeID); got != tt.wantThisEdge {
				t.Errorf("WrittenBy: got %t, want %t", got, tt.wantThisEdge)
			}
		})
	}
}

func TestOccurrenceID(t *testing.T) {
	t.Parallel()

	start := provider.Zoned(time.Date(2026, time.March, 11, 9, 0, 0, 0, time.UTC), "Europe/Berlin")

	id := occurrenceID("evt-1", start)
	if id != "evt-1#20260311T090000" {
		t.Fatalf("occurrence id: got %q", id)
	}
	if !isOccurrenceOf(id, "evt-1") {
		t.Fatal("the occurrence does not belong to its own master")
	}
	if isOccurrenceOf(id, "evt-2") {
		t.Fatal("the occurrence belongs to another master")
	}
	if isOccurrenceOf("evt-1", "evt-1") {
		t.Fatal("the master itself reads as one of its own occurrences")
	}
}
