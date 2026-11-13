// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// IsOwnWrite reports whether calbridge wrote the event. The marker names
// the edge that wrote it, so an event carrying one is a copy and never a
// source change, whichever edge made it.
func IsOwnWrite(event provider.Event) bool { return event.Marker.Edge != "" }

// WrittenBy reports whether the event is a copy this edge made.
func WrittenBy(event provider.Event, edgeID uuid.UUID) bool {
	return event.Marker.Edge == edgeID.String()
}

// contentHash is what decides whether a write is needed. It covers
// everything a copy carries: the mask that produced it, the two
// endpoints, the copied fields and the marker. It covers nothing the
// provider owns, because the provider's id, change tag and timestamps
// differ on every read and say nothing about the content.
//
// The same function hashes the projection the engine is about to write
// and the event the target hands back, which is what lets a reconcile
// recognise an event somebody edited by hand.
func contentHash(event provider.Event, mask FieldMask) string {
	sum := sha256.New()

	write := func(format string, args ...any) {
		// Writing to a hash never fails, which is why hash.Hash's own Write
		// is documented as returning a nil error.
		_, _ = fmt.Fprintf(sum, format, args...)
	}

	write("mask\x00%s\x00", mask)
	write("start\x00%s\x00", event.Start)
	write("end\x00%s\x00", event.End)
	write("summary\x00%s\x00", event.Summary)
	write("description\x00%s\x00", event.Description)
	write("location\x00%s\x00", event.Location)
	write("status\x00%s\x00", event.Status)
	write("transparency\x00%s\x00", event.Transparency)
	write("visibility\x00%s\x00", event.Visibility)
	write("marker\x00%s\x00%s\x00", event.Marker.Edge, event.Marker.Source)

	for _, a := range event.Attendees {
		write("attendee\x00%s\x00%s\x00%t\x00%s\x00", a.Email, a.Name, a.Optional, a.Response)
	}
	for _, line := range event.Recurrence.Rule {
		write("rule\x00%s\x00", line)
	}

	return hex.EncodeToString(sum.Sum(nil))
}

// cachedDetails is the copied title and body the mapping row holds. The
// database stores it encrypted, so the mapping table alone does not read
// as a calendar, and a busy block edge stores nothing at all.
type cachedDetails struct {
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
}

// details returns the copied content to cache with the mapping row, or
// nil where the mask copies no content worth caching.
func details(event provider.Event, mask FieldMask) ([]byte, error) {
	cached := cachedDetails{}
	if mask.Has(FieldSummary) {
		cached.Summary = event.Summary
	}
	if mask.Has(FieldDescription) {
		cached.Description = event.Description
	}
	if mask.Has(FieldLocation) {
		cached.Location = event.Location
	}
	if cached == (cachedDetails{}) {
		return nil, nil
	}

	encoded, err := json.Marshal(cached)
	if err != nil {
		return nil, fmt.Errorf("encode cached details: %w", err)
	}

	return encoded, nil
}

// instant resolves an event time to the moment the mapping row records.
// A zoned time is resolved in its own zone, and a date or a floating time
// is recorded as it reads, which is what a calendar means by them.
func instant(t provider.EventTime) (time.Time, error) {
	if t.Kind != provider.TimeKindZoned {
		return t.Wall, nil
	}

	loc, err := time.LoadLocation(t.Zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load zone %q: %w", t.Zone, err)
	}
	w := t.Wall

	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, loc), nil
}

// occurrenceSeparator joins a recurring master's event id to one
// occurrence's start, forming the source event id an expanded instance is
// mapped under. A provider's own event id is opaque, so an id already
// containing the separator would collide with an occurrence of another
// event; no provider calbridge targets issues one, and a collision would
// show as one copy serving two source events rather than as lost data.
const occurrenceSeparator = "#"

// occurrenceID is the source event id one expanded occurrence is mapped
// under. It is stable for the occurrence's place in the series, so an
// occurrence moved to another time keeps the same mapping row.
func occurrenceID(masterID string, start provider.EventTime) string {
	return masterID + occurrenceSeparator + start.Wall.Format("20060102T150405")
}

// isOccurrenceOf reports whether a mapped source event id is one
// occurrence the expansion of masterID produced.
func isOccurrenceOf(sourceEventID, masterID string) bool {
	return strings.HasPrefix(sourceEventID, masterID+occurrenceSeparator)
}
