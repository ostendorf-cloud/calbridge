// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// Field is one piece of an event a detail level either copies or drops.
// Start and end are not fields here: every copy carries the source's time,
// because a copy that does not is not a copy of an event.
type Field uint16

// The fields a detail level decides about.
const (
	// FieldSummary is the title.
	FieldSummary Field = 1 << iota
	// FieldDescription is the body text.
	FieldDescription
	// FieldLocation is the free text location.
	FieldLocation
	// FieldAttendees is the invited addresses.
	FieldAttendees
	// FieldStatus is whether the event is happening.
	FieldStatus
	// FieldTransparency is whether the event occupies its time. A busy
	// block is nothing but this, so it is the one field every preset
	// copies.
	FieldTransparency
	// FieldVisibility is who may read the event's detail.
	FieldVisibility
	// FieldRecurrence is the repeating rule. Dropping it does not drop the
	// series: the engine then expands it into instances, the same way it
	// does for a target that stores no rule.
	FieldRecurrence
)

// allFields lists every field once, in the order a mask renders and
// hashes them, so two equal masks never hash differently.
var allFields = []Field{
	FieldSummary,
	FieldDescription,
	FieldLocation,
	FieldAttendees,
	FieldStatus,
	FieldTransparency,
	FieldVisibility,
	FieldRecurrence,
}

// String returns the field's name.
func (f Field) String() string {
	switch f {
	case FieldSummary:
		return "summary"
	case FieldDescription:
		return "description"
	case FieldLocation:
		return "location"
	case FieldAttendees:
		return "attendees"
	case FieldStatus:
		return "status"
	case FieldTransparency:
		return "transparency"
	case FieldVisibility:
		return "visibility"
	case FieldRecurrence:
		return "recurrence"
	default:
		return fmt.Sprintf("Field(%d)", uint16(f))
	}
}

// FieldMask is what one edge copies. A preset builds one, and With and
// Without override it field by field, which is how a user keeps the busy
// block preset but asks for the location as well.
//
// The presets nest: busy block is part of title and time, which is part
// of a full copy. Within relies on that, and so does the check that an
// edge stays inside the grant that permits it.
type FieldMask struct {
	fields Field
}

// The preset masks the design names. A busy block carries only whether
// the time is taken and whether the event is happening at all. Title and
// time adds the title, the repeating rule and the visibility. A full
// copy adds everything else this package knows about.
//
// The visibility travels with the title on purpose: an event the source
// marks private is one whose title the owner already restricted, and a
// copy that drops the restriction while keeping the title shows it to
// everyone the target calendar is shared with. A busy block carries no
// title to restrict.
var (
	maskBusyBlock = FieldMask{fields: FieldStatus | FieldTransparency}
	maskTitleTime = FieldMask{fields: maskBusyBlock.fields | FieldSummary | FieldRecurrence | FieldVisibility}
	maskFull      = FieldMask{fields: maskTitleTime.fields | FieldDescription | FieldLocation | FieldAttendees}
)

// MaskFor returns the preset mask for a detail level.
func MaskFor(level storage.DetailLevel) (FieldMask, error) {
	switch level {
	case storage.DetailLevelFull:
		return maskFull, nil
	case storage.DetailLevelTitleTime:
		return maskTitleTime, nil
	case storage.DetailLevelBusyBlock:
		return maskBusyBlock, nil
	default:
		return FieldMask{}, fmt.Errorf("detail level %q: %w", string(level), ErrInvalidDetailLevel)
	}
}

// With returns the mask with the given fields copied as well.
func (m FieldMask) With(fields ...Field) FieldMask {
	for _, f := range fields {
		m.fields |= f
	}

	return m
}

// Without returns the mask with the given fields dropped.
func (m FieldMask) Without(fields ...Field) FieldMask {
	for _, f := range fields {
		m.fields &^= f
	}

	return m
}

// Has reports whether the mask copies the field.
func (m FieldMask) Has(f Field) bool { return m.fields&f == f }

// Within reports whether the mask copies no more than other does. It is
// what bounds an edge by the grant that permits it: a grant's maximum
// detail level is a mask, and an edge may copy any subset of it.
func (m FieldMask) Within(other FieldMask) bool { return m.fields&^other.fields == 0 }

// Fields returns the copied fields, in a fixed order.
func (m FieldMask) Fields() []Field {
	out := make([]Field, 0, len(allFields))
	for _, f := range allFields {
		if m.Has(f) {
			out = append(out, f)
		}
	}

	return out
}

// String renders the mask as its field names, for a log line or a test
// failure.
func (m FieldMask) String() string {
	names := make([]string, 0, len(allFields))
	for _, f := range m.Fields() {
		names = append(names, f.String())
	}
	if len(names) == 0 {
		return "none"
	}

	return strings.Join(names, ",")
}

// project builds the event this edge writes into target from the source
// event. It copies the masked fields, drops what the target's
// capabilities say it cannot keep, anchors a floating time the target
// cannot store, and stamps the marker.
//
// Dropping what the target cannot keep here, rather than leaving it to
// the connector, is what keeps the content hash honest: the hash of the
// projection has to match the hash of the event the provider gives back,
// or every reconcile would read a difference and rewrite. A target that
// rewrites content of its own declares it, and Binding.stored is what
// then says what the comparison runs against.
//
// A projection is never an instance. The recurrence a copy carries is a
// rule or nothing: Recurrence.MasterID and Recurrence.OriginalStart
// describe the source's own series and mean nothing in the target. An
// overridden occurrence the engine copies on its own is written as a
// plain event at its own time, which is why a target declaring
// Capabilities.InstanceWrites as false is never asked for a write it
// would refuse.
func (m FieldMask) project(src provider.Event, target Endpoint, marker provider.Marker) (provider.Event, error) {
	caps := target.Connector.Capabilities()

	start, err := anchor(src.Start, caps, target.TimeZone)
	if err != nil {
		return provider.Event{}, fmt.Errorf("start: %w", err)
	}
	end, err := anchor(src.End, caps, target.TimeZone)
	if err != nil {
		return provider.Event{}, fmt.Errorf("end: %w", err)
	}

	out := provider.Event{Start: start, End: end, Marker: marker}
	if m.Has(FieldSummary) {
		out.Summary = src.Summary
	}
	if m.Has(FieldDescription) {
		out.Description = src.Description
	}
	if m.Has(FieldLocation) {
		out.Location = src.Location
	}
	if m.Has(FieldStatus) {
		out.Status = src.Status
	}
	if m.Has(FieldTransparency) {
		out.Transparency = src.Transparency
	}
	if m.Has(FieldVisibility) {
		out.Visibility = src.Visibility
	}
	if m.Has(FieldAttendees) && caps.Attendees {
		out.Attendees = slices.Clone(src.Attendees)
	}
	if m.Has(FieldRecurrence) && caps.NativeRecurrence {
		out.Recurrence.Rule = slices.Clone(src.Recurrence.Rule)
	}

	return out, nil
}

// anchor puts a floating time into the target calendar's zone where the
// target stores no floating time. The capability declaration decides,
// never a guess about the provider, and a target calendar with no zone of
// its own leaves nothing to anchor in.
func anchor(t provider.EventTime, caps provider.Capabilities, zone string) (provider.EventTime, error) {
	if t.Kind != provider.TimeKindFloating || caps.FloatingTimes {
		return t, nil
	}
	if zone == "" {
		return provider.EventTime{}, ErrNoTargetZone
	}

	return provider.Zoned(t.Wall, zone), nil
}
