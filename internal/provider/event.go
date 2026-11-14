// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider

import (
	"fmt"
	"strings"
	"time"
)

// TimeKind says how an EventTime is anchored. Its zero value is invalid,
// so an endpoint a connector forgot to map is caught by Validate instead
// of being read as a date.
type TimeKind uint8

const (
	// TimeKindUnset is the zero value and never valid.
	TimeKindUnset TimeKind = iota
	// TimeKindDate is a calendar date with no time and no zone, which is
	// how an all-day event is carried.
	TimeKindDate
	// TimeKindZoned is a wall time in the IANA zone EventTime.Zone names.
	TimeKindZoned
	// TimeKindFloating is a wall time in no zone: it means the same clock
	// reading wherever the reader is.
	TimeKindFloating
)

// String returns the kind's name.
func (k TimeKind) String() string {
	switch k {
	case TimeKindUnset:
		return "unset"
	case TimeKindDate:
		return "date"
	case TimeKindZoned:
		return "zoned"
	case TimeKindFloating:
		return "floating"
	default:
		return fmt.Sprintf("TimeKind(%d)", uint8(k))
	}
}

// EventTime is one endpoint of an event, in one of the three forms a
// calendar can hold. See the normalisation rules in the package
// documentation for which form a connector picks.
type EventTime struct {
	// Kind says which of the three forms this endpoint takes, and which
	// of the fields below carry meaning.
	Kind TimeKind
	// Wall carries the civil fields: the date for every kind, and the
	// time of day for TimeKindZoned and TimeKindFloating. Its location is
	// always time.UTC and says nothing about the event's zone, so two
	// wall times are compared directly. Zone carries the zone.
	Wall time.Time
	// Zone is the IANA zone name for TimeKindZoned, such as
	// "Europe/Berlin". It is empty for every other kind.
	Zone string
}

// Date returns an all-day endpoint on the given calendar date.
func Date(year int, month time.Month, day int) EventTime {
	return EventTime{
		Kind: TimeKindDate,
		Wall: time.Date(year, month, day, 0, 0, 0, 0, time.UTC),
	}
}

// Zoned returns an endpoint at the wall clock reading of wall, in the
// IANA zone name zone. Only the civil fields of wall are read, so the
// location it carries does not matter.
func Zoned(wall time.Time, zone string) EventTime {
	return EventTime{Kind: TimeKindZoned, Wall: civil(wall), Zone: zone}
}

// Floating returns an endpoint at the wall clock reading of wall that
// belongs to no zone. Only the civil fields of wall are read.
func Floating(wall time.Time) EventTime {
	return EventTime{Kind: TimeKindFloating, Wall: civil(wall)}
}

// civil strips a time's location, keeping the clock reading a user sees.
func civil(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// IsZero reports whether the endpoint was never set.
func (t EventTime) IsZero() bool {
	return t.Kind == TimeKindUnset && t.Wall.IsZero() && t.Zone == ""
}

// Equal reports whether two endpoints are the same reading in the same
// form. It is not a comparison of instants: two zoned times in different
// zones are never equal here, even where they name the same moment.
func (t EventTime) Equal(other EventTime) bool {
	return t.Kind == other.Kind && t.Wall.Equal(other.Wall) && t.Zone == other.Zone
}

// Before reports whether t is earlier than other, and whether that
// question could be answered at all. Two endpoints compare when they
// have the same kind and, for a zoned time, the same zone. Resolving two
// different zones against each other needs the zone database, which is
// the sync engine's business and not this package's.
func (t EventTime) Before(other EventTime) (before, known bool) {
	if t.Kind != other.Kind || t.Kind == TimeKindUnset {
		return false, false
	}
	if t.Kind == TimeKindZoned && t.Zone != other.Zone {
		return false, false
	}

	return t.Wall.Before(other.Wall), true
}

// Validate reports whether the endpoint obeys the normalisation rules.
func (t EventTime) Validate() error {
	switch t.Kind {
	case TimeKindUnset:
		return fmt.Errorf("time kind is unset: %w", ErrInvalidEvent)
	case TimeKindDate:
		if t.Zone != "" {
			return fmt.Errorf("all-day time carries zone %q: %w", t.Zone, ErrInvalidEvent)
		}
		if h, m, s := t.Wall.Clock(); h != 0 || m != 0 || s != 0 {
			return fmt.Errorf("all-day time carries a time of day: %w", ErrInvalidEvent)
		}
	case TimeKindZoned:
		if t.Zone == "" {
			return fmt.Errorf("zoned time names no zone: %w", ErrInvalidEvent)
		}
	case TimeKindFloating:
		if t.Zone != "" {
			return fmt.Errorf("floating time carries zone %q: %w", t.Zone, ErrInvalidEvent)
		}
	default:
		return fmt.Errorf("time kind %d is not one of the three: %w", uint8(t.Kind), ErrInvalidEvent)
	}
	if t.Wall.IsZero() {
		return fmt.Errorf("time carries no date: %w", ErrInvalidEvent)
	}

	return nil
}

// String renders the endpoint for a log line or a test failure.
func (t EventTime) String() string {
	switch t.Kind {
	case TimeKindDate:
		return t.Wall.Format("2006-01-02")
	case TimeKindZoned:
		return t.Wall.Format("2006-01-02T15:04:05") + " " + t.Zone
	case TimeKindFloating:
		return t.Wall.Format("2006-01-02T15:04:05") + " floating"
	case TimeKindUnset:
		return "unset"
	default:
		return fmt.Sprintf("TimeKind(%d)", uint8(t.Kind))
	}
}

// Status is whether the event is happening. Every provider carries these
// three, so no mapping loses anything.
type Status uint8

const (
	// StatusConfirmed is the zero value: the event is happening.
	StatusConfirmed Status = iota
	// StatusTentative means the event is not settled yet.
	StatusTentative
	// StatusCancelled means the event was called off but is still listed.
	// A change feed reports a removed event as a ChangeKindDelete rather
	// than with this status.
	StatusCancelled
)

// String returns the status's name.
func (s Status) String() string {
	switch s {
	case StatusConfirmed:
		return "confirmed"
	case StatusTentative:
		return "tentative"
	case StatusCancelled:
		return "cancelled"
	default:
		return fmt.Sprintf("Status(%d)", uint8(s))
	}
}

// Valid reports whether the status is one of the three.
func (s Status) Valid() bool { return s <= StatusCancelled }

// Transparency is whether the event occupies its time. It is what a busy
// block depends on, so it is copied even at the lowest detail level.
type Transparency uint8

const (
	// TransparencyOpaque is the zero value: the time is taken.
	TransparencyOpaque Transparency = iota
	// TransparencyTransparent means the time stays free.
	TransparencyTransparent
)

// String returns the transparency's name.
func (t Transparency) String() string {
	switch t {
	case TransparencyOpaque:
		return "opaque"
	case TransparencyTransparent:
		return "transparent"
	default:
		return fmt.Sprintf("Transparency(%d)", uint8(t))
	}
}

// Valid reports whether the transparency is one of the two.
func (t Transparency) Valid() bool { return t <= TransparencyTransparent }

// Visibility is who may read the event's detail, as far as the provider
// expresses it.
type Visibility uint8

const (
	// VisibilityDefault is the zero value: the calendar's own setting
	// decides.
	VisibilityDefault Visibility = iota
	// VisibilityPublic means anyone who reaches the calendar reads the
	// detail.
	VisibilityPublic
	// VisibilityPrivate means only the owner reads the detail.
	VisibilityPrivate
	// VisibilityConfidential means the event is shown without its detail.
	VisibilityConfidential
)

// String returns the visibility's name.
func (v Visibility) String() string {
	switch v {
	case VisibilityDefault:
		return "default"
	case VisibilityPublic:
		return "public"
	case VisibilityPrivate:
		return "private"
	case VisibilityConfidential:
		return "confidential"
	default:
		return fmt.Sprintf("Visibility(%d)", uint8(v))
	}
}

// Valid reports whether the visibility is one of the four.
func (v Visibility) Valid() bool { return v <= VisibilityConfidential }

// Response is an attendee's answer to the invitation.
type Response uint8

const (
	// ResponseNeedsAction is the zero value: the attendee has not
	// answered.
	ResponseNeedsAction Response = iota
	// ResponseAccepted means the attendee is coming.
	ResponseAccepted
	// ResponseDeclined means the attendee is not coming.
	ResponseDeclined
	// ResponseTentative means the attendee may be coming.
	ResponseTentative
)

// String returns the response's name.
func (r Response) String() string {
	switch r {
	case ResponseNeedsAction:
		return "needs-action"
	case ResponseAccepted:
		return "accepted"
	case ResponseDeclined:
		return "declined"
	case ResponseTentative:
		return "tentative"
	default:
		return fmt.Sprintf("Response(%d)", uint8(r))
	}
}

// Valid reports whether the response is one of the four.
func (r Response) Valid() bool { return r <= ResponseTentative }

// Attendee is one invited address. A connector returns the attendees it
// can read and drops the provider's own extras, such as a resource flag
// or a delegation chain, because no target can be trusted to hold them.
type Attendee struct {
	// Email is the attendee's address, and the only field every provider
	// guarantees.
	Email string
	// Name is the display name, empty where the provider has none.
	Name string
	// Optional reports that attendance is not required.
	Optional bool
	// Response is the attendee's answer.
	Response Response
}

// Recurrence says how an event repeats, and which half of a repeating
// series this event is.
type Recurrence struct {
	// Rule holds the RFC 5545 recurrence lines of a repeating master:
	// RRULE, EXRULE, RDATE and EXDATE, one per entry, without DTSTART or
	// DTEND. It is empty on a single event and on an instance.
	Rule []string
	// MasterID is set on an instance only, and holds the Event.ID of the
	// master it belongs to.
	MasterID string
	// OriginalStart is set on an instance only, and is the start the
	// master's rule gives it. It identifies the instance even after the
	// instance was moved to another time.
	OriginalStart EventTime
}

// InstanceSeparator joins a master's Event.ID to the provider's own
// identifier of one overridden occurrence. A provider that stores its
// overrides inside the master's object has no id of its own to give them,
// so the connector derives one, and this is the byte that derives it.
//
// Reading a derived id back is only unambiguous where no plain event id
// in the same calendar carries the separator, and a connector does not
// always get to choose. A CalDAV resource name is the server's, not the
// connector's: a resource named "a%23b.ics" reaches the connector as the
// event id "a#b.ics" and reads as an occurrence of "a". A caller may not
// assume the separator is absent from a plain id. What it may assume is
// the weaker thing: a collision shows as one event's copy being swept
// with another's, never as a source event the caller stops seeing.
// Closing it for good is issue #29, which bounds the damage in the
// engine rather than by constraining what a server may name a resource.
const InstanceSeparator = "#"

// InstanceIDOf returns the Event.ID of one overridden occurrence of the
// master masterID names. occurrence is whatever the provider identifies
// the occurrence by, such as an RFC 5545 RECURRENCE-ID, and stays opaque
// outside the connector.
func InstanceIDOf(masterID, occurrence string) string {
	return masterID + InstanceSeparator + occurrence
}

// IsInstanceIDOf reports whether eventID is an id derived from masterID
// with InstanceIDOf. It is what turns one deletion of a master into the
// deletion of the instances the provider removed with it.
//
// It answers on the id's shape alone, so a plain event id that happens to
// carry the separator answers true for whatever precedes it. See
// InstanceSeparator for what that costs and what it does not.
func IsInstanceIDOf(eventID, masterID string) bool {
	return strings.HasPrefix(eventID, masterID+InstanceSeparator)
}

// IsMaster reports whether the event repeats under its own rule.
func (r Recurrence) IsMaster() bool { return len(r.Rule) > 0 }

// IsInstance reports whether the event is one occurrence of a master.
func (r Recurrence) IsInstance() bool { return r.MasterID != "" }

// Validate reports whether the recurrence is one of the three shapes a
// connector may return: no recurrence, a master with rules, or an
// instance naming its master.
func (r Recurrence) Validate() error {
	if r.IsMaster() && r.IsInstance() {
		return fmt.Errorf("event carries both a recurrence rule and a master id: %w", ErrInvalidEvent)
	}
	if !r.IsInstance() && !r.OriginalStart.IsZero() {
		return fmt.Errorf("event carries an original start without a master id: %w", ErrInvalidEvent)
	}
	if r.IsInstance() && r.OriginalStart.IsZero() {
		return fmt.Errorf("instance carries no original start: %w", ErrInvalidEvent)
	}
	if r.IsInstance() {
		if err := r.OriginalStart.Validate(); err != nil {
			return fmt.Errorf("original start: %w", err)
		}
	}

	return nil
}

// Marker is the tag calbridge puts on every event it writes, so a
// connector never reports its own write back as a source change, and so a
// deleted edge finds what it wrote. A connector stores both fields with
// the event and returns them unchanged. What they mean belongs to the
// sync engine, not here.
//
// It carries no detail level and no field mask, and that is deliberate.
// The two fields here are an identity, and a connector has to store them
// in whatever it has for an extension property. Adding what a copy was
// made at would put a sync concept in every connector and would be a
// second place for it to be wrong. The engine folds the mask into the
// content hash it stores with the copy instead, so a copy made at another
// detail level hashes differently and is rewritten, and the stored row,
// not the provider, is what says what the copy was made at.
type Marker struct {
	// Edge names the sync edge that wrote the event.
	Edge string
	// Source names the event the copy was made from, in the source
	// calendar.
	Source string
}

// IsZero reports whether the event carries no marker, which means
// calbridge did not write it.
func (m Marker) IsZero() bool { return m == Marker{} }

// Event is one calendar entry in the normalised form every connector
// speaks. Every field a detail level can copy or drop is here, and
// nothing provider shaped is.
type Event struct {
	// ID is the provider's own id for the event in its calendar. It is
	// opaque outside the connector, and is empty on an event being
	// created.
	ID string
	// UID is the RFC 5545 UID, which is stable across a move between
	// calendars where the provider exposes it, and empty where it does
	// not.
	UID string
	// ETag is the provider's opaque change tag, used to write only when
	// the event has not changed underneath. It is empty where the
	// provider has none.
	ETag string
	// Summary is the title. It may be empty: an event copied as a busy
	// block carries no title.
	Summary string
	// Description is the body text, in plain text. A connector strips the
	// provider's markup rather than passing it on, because the three do
	// not agree on one.
	Description string
	// Location is the free text location.
	Location string
	// Start is when the event begins.
	Start EventTime
	// End is the first moment the event no longer occupies. For an
	// all-day event that is the day after the last day.
	End EventTime
	// Status is whether the event is happening.
	Status Status
	// Transparency is whether the event occupies its time.
	Transparency Transparency
	// Visibility is who may read the event's detail.
	Visibility Visibility
	// Recurrence says how the event repeats, if it does.
	Recurrence Recurrence
	// Attendees are the invited addresses. A connector whose Capabilities
	// report Attendees as false leaves it empty and drops it on write.
	Attendees []Attendee
	// Organizer is the address the provider names as the organiser. A
	// connector reports it and ignores it on write, because every
	// provider sets the organiser from the writing account.
	Organizer *Attendee
	// Marker is calbridge's own tag, set on an event calbridge wrote and
	// zero on every other event.
	Marker Marker
	// Created is when the provider first stored the event, zero where the
	// provider does not report it.
	Created time.Time
	// Updated is when the provider last changed the event, zero where the
	// provider does not report it.
	Updated time.Time
}

// Validate reports whether the event obeys the normalisation rules. A
// connector validates what it returns, and a caller validates what it is
// about to write, so a broken mapping is caught at the boundary that
// owns it.
func (e Event) Validate() error {
	if err := e.Start.Validate(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	if err := e.End.Validate(); err != nil {
		return fmt.Errorf("end: %w", err)
	}
	if e.Start.Kind != e.End.Kind {
		return fmt.Errorf("start is %s and end is %s: %w", e.Start.Kind, e.End.Kind, ErrInvalidEvent)
	}
	if before, ok := e.End.Before(e.Start); ok && before {
		return fmt.Errorf("end %s is before start %s: %w", e.End, e.Start, ErrInvalidEvent)
	}
	if e.Start.Kind == TimeKindDate && e.Start.Wall.Equal(e.End.Wall) {
		return fmt.Errorf("all-day event ends on its start date %s, and end is exclusive: %w", e.Start, ErrInvalidEvent)
	}
	if !e.Status.Valid() {
		return fmt.Errorf("status %s: %w", e.Status, ErrInvalidEvent)
	}
	if !e.Transparency.Valid() {
		return fmt.Errorf("transparency %s: %w", e.Transparency, ErrInvalidEvent)
	}
	if !e.Visibility.Valid() {
		return fmt.Errorf("visibility %s: %w", e.Visibility, ErrInvalidEvent)
	}
	for i, a := range e.Attendees {
		if a.Email == "" {
			return fmt.Errorf("attendee %d carries no address: %w", i, ErrInvalidEvent)
		}
		if !a.Response.Valid() {
			return fmt.Errorf("attendee %d response %s: %w", i, a.Response, ErrInvalidEvent)
		}
	}
	if err := e.Recurrence.Validate(); err != nil {
		return fmt.Errorf("recurrence: %w", err)
	}

	return nil
}
