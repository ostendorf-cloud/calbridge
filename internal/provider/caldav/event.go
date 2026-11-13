// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// The X- properties calbridge keeps its own marker in. RFC 5545 section
// 3.8.8.2 reserves the X- prefix for exactly this, and a CalDAV server
// stores the object as written, so the marker comes back on every later
// read.
const (
	markerEdgeProp   = "X-CALBRIDGE-EDGE"
	markerSourceProp = "X-CALBRIDGE-SOURCE"
)

// instanceSeparator joins a resource name and a RECURRENCE-ID into the
// id of one overridden occurrence. It is a character no path segment of
// a CalDAV resource carries unescaped, so splitting an id is
// unambiguous.
const instanceSeparator = "#"

// productID is the PRODID every object this connector writes carries,
// as RFC 5545 section 3.7.3 requires.
const productID = "-//ostendorf-cloud//calbridge//EN"

// timezoneName reads the zone a calendar's CALDAV:calendar-timezone
// property names. RFC 4791 section 5.2.2 carries that property as an
// iCalendar object holding one VTIMEZONE, so the zone is the TZID
// inside it, and only a name the zone database knows is reported.
func timezoneName(property string) string {
	if strings.TrimSpace(property) == "" {
		return ""
	}
	root, err := parseICalendar(property)
	if err != nil {
		return ""
	}
	for _, vtimezone := range root.children("VTIMEZONE") {
		tzid := normaliseTZID(vtimezone.value("TZID"))
		if tzid == "" {
			continue
		}
		if _, err := time.LoadLocation(tzid); err != nil {
			continue
		}

		return tzid
	}

	return ""
}

// decodeResource turns one calendar object resource into the events it
// holds. A resource holds one UID: the recurring master, where there is
// one, and a VEVENT for each occurrence that was overridden.
func decodeResource(resourceName, text string) ([]provider.Event, error) {
	root, err := parseICalendar(text)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(root.Name, "VCALENDAR") {
		return nil, fmt.Errorf("calendar object is a %s, not a VCALENDAR: %w", root.Name, ErrServer)
	}

	zones := locationsIn(root)
	var out []provider.Event
	for _, component := range root.children("VEVENT") {
		event, err := decodeEvent(resourceName, component, zones)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("calendar object %q holds no VEVENT: %w", resourceName, ErrServer)
	}

	return out, nil
}

// locationsIn reads the TZIDs the object defines a VTIMEZONE for, so a
// TZID that is not an IANA zone name can still be recognised as one the
// object itself describes.
func locationsIn(root *icalComponent) map[string]bool {
	out := map[string]bool{}
	for _, vtimezone := range root.children("VTIMEZONE") {
		if tzid := vtimezone.value("TZID"); tzid != "" {
			out[normaliseTZID(tzid)] = true
		}
	}

	return out
}

// decodeEvent maps one VEVENT onto the normalised event.
func decodeEvent(resourceName string, component *icalComponent, zones map[string]bool) (provider.Event, error) {
	start, err := eventTime(component, "DTSTART", zones)
	if err != nil {
		return provider.Event{}, err
	}
	end, err := eventEnd(component, start, zones)
	if err != nil {
		return provider.Event{}, err
	}

	event := provider.Event{
		ID:           resourceName,
		UID:          component.value("UID"),
		Summary:      component.value("SUMMARY"),
		Description:  component.value("DESCRIPTION"),
		Location:     component.value("LOCATION"),
		Start:        start,
		End:          end,
		Status:       decodeStatus(component.value("STATUS")),
		Transparency: decodeTransparency(component.value("TRANSP")),
		Visibility:   decodeVisibility(component.value("CLASS")),
		Attendees:    decodeAttendees(component),
		Organizer:    decodeOrganizer(component),
		Marker: provider.Marker{
			Edge:   component.value(markerEdgeProp),
			Source: component.value(markerSourceProp),
		},
		Created: decodeTimestamp(component.value("CREATED")),
		Updated: decodeTimestamp(component.value("LAST-MODIFIED")),
	}

	if recurrenceID, ok := component.prop("RECURRENCE-ID"); ok {
		original, err := timeFromLine(recurrenceID, zones)
		if err != nil {
			return provider.Event{}, err
		}
		event.ID = resourceName + instanceSeparator + recurrenceID.Value
		event.Recurrence.MasterID = resourceName
		event.Recurrence.OriginalStart = original
	} else {
		event.Recurrence.Rule = decodeRecurrenceRule(component)
	}

	if err := event.Validate(); err != nil {
		return provider.Event{}, fmt.Errorf("calendar object %q: %w", resourceName, err)
	}

	return event, nil
}

// recurrenceProps are the RFC 5545 properties that make up a repeating
// master's rule, in the order the normalised event carries them.
var recurrenceProps = []string{"RRULE", "EXRULE", "RDATE", "EXDATE"}

// decodeRecurrenceRule rebuilds the recurrence lines as the normalised
// event carries them: one content line per entry, with DTSTART and DTEND
// left out.
func decodeRecurrenceRule(component *icalComponent) []string {
	var out []string
	for _, name := range recurrenceProps {
		for _, line := range component.props(name) {
			out = append(out, renderLine(line))
		}
	}

	return out
}

// renderLine writes one parsed content line back out, unfolded, with its
// parameters in a stable order.
func renderLine(line icalLine) string {
	var b strings.Builder
	b.WriteString(line.Name)
	for _, key := range sortedKeys(line.Params) {
		b.WriteString(";")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(quoteParam(strings.Join(line.Params[key], ",")))
	}
	b.WriteString(":")
	b.WriteString(line.Value)

	return b.String()
}

// sortedKeys returns a map's keys in a stable order.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	return out
}

// eventTime reads one date or date-time property off a component.
func eventTime(component *icalComponent, name string, zones map[string]bool) (provider.EventTime, error) {
	line, ok := component.prop(name)
	if !ok {
		return provider.EventTime{}, fmt.Errorf("VEVENT carries no %s: %w", name, ErrServer)
	}

	return timeFromLine(line, zones)
}

// eventEnd reads the event's end. RFC 5545 section 3.6.1 fills in the
// two cases where a VEVENT carries neither DTEND nor DURATION: an
// all-day event lasts one day, and a timed event ends where it starts.
func eventEnd(component *icalComponent, start provider.EventTime, zones map[string]bool) (provider.EventTime, error) {
	if line, ok := component.prop("DTEND"); ok {
		return timeFromLine(line, zones)
	}
	if line, ok := component.prop("DURATION"); ok {
		d, err := parseDuration(line.Value)
		if err != nil {
			return provider.EventTime{}, err
		}
		end := start
		end.Wall = start.Wall.Add(d)

		return end, nil
	}

	end := start
	if start.Kind == provider.TimeKindDate {
		end.Wall = start.Wall.AddDate(0, 0, 1)
	}

	return end, nil
}

// timeFromLine maps one content line onto the three forms RFC 5545
// section 3.3.5 defines, which are the three the normalised event takes.
func timeFromLine(line icalLine, zones map[string]bool) (provider.EventTime, error) {
	value := strings.TrimSpace(line.Value)
	if strings.EqualFold(line.param("VALUE"), "DATE") || (len(value) == 8 && !strings.Contains(value, "T")) {
		day, err := time.Parse("20060102", value)
		if err != nil {
			return provider.EventTime{}, fmt.Errorf("%s carries no date: %w", line.Name, ErrServer)
		}

		return provider.EventTime{Kind: provider.TimeKindDate, Wall: day}, nil
	}

	if strings.HasSuffix(value, "Z") {
		wall, err := time.Parse("20060102T150405Z", value)
		if err != nil {
			return provider.EventTime{}, fmt.Errorf("%s carries no utc date-time: %w", line.Name, ErrServer)
		}

		return provider.EventTime{Kind: provider.TimeKindZoned, Wall: wall, Zone: "UTC"}, nil
	}

	wall, err := time.Parse("20060102T150405", value)
	if err != nil {
		return provider.EventTime{}, fmt.Errorf("%s carries no date-time: %w", line.Name, ErrServer)
	}
	tzid := normaliseTZID(line.param("TZID"))
	if tzid == "" {
		return provider.EventTime{Kind: provider.TimeKindFloating, Wall: wall}, nil
	}
	if _, err := time.LoadLocation(tzid); err != nil && !zones[tzid] {
		// A TZID the zone database does not know and the object does not
		// describe cannot be resolved by anything above this package, and
		// reading it as a zone name would put the event in the wrong
		// place. A wall time in no zone is the honest reading.
		return provider.EventTime{Kind: provider.TimeKindFloating, Wall: wall}, nil
	}

	return provider.EventTime{Kind: provider.TimeKindZoned, Wall: wall, Zone: tzid}, nil
}

// normaliseTZID strips the leading solidus RFC 5545 section 3.2.19
// allows in front of a globally defined zone name.
func normaliseTZID(tzid string) string {
	return strings.TrimPrefix(strings.TrimSpace(tzid), "/")
}

// parseDuration reads the duration form of RFC 5545 section 3.3.6, for
// example "P15DT5H0M20S". A week form carries no other field.
func parseDuration(value string) (time.Duration, error) {
	raw := strings.TrimSpace(value)
	sign := time.Duration(1)
	switch {
	case strings.HasPrefix(raw, "-"):
		sign, raw = -1, raw[1:]
	case strings.HasPrefix(raw, "+"):
		raw = raw[1:]
	}
	if !strings.HasPrefix(raw, "P") {
		return 0, fmt.Errorf("duration %q does not start with P: %w", value, ErrServer)
	}
	raw = raw[1:]

	units := map[byte]time.Duration{
		'W': 7 * 24 * time.Hour,
		'D': 24 * time.Hour,
		'H': time.Hour,
		'M': time.Minute,
		'S': time.Second,
	}

	var total time.Duration
	var digits strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= '0' && c <= '9':
			digits.WriteByte(c)
		case c == 'T':
			if digits.Len() != 0 {
				return 0, fmt.Errorf("duration %q: %w", value, ErrServer)
			}
		default:
			unit, ok := units[c]
			if !ok || digits.Len() == 0 {
				return 0, fmt.Errorf("duration %q: %w", value, ErrServer)
			}
			n, err := strconv.Atoi(digits.String())
			if err != nil {
				return 0, fmt.Errorf("duration %q: %w", value, ErrServer)
			}
			digits.Reset()
			total += time.Duration(n) * unit
		}
	}
	if digits.Len() != 0 {
		return 0, fmt.Errorf("duration %q ends without a unit: %w", value, ErrServer)
	}

	return sign * total, nil
}

// decodeStatus maps the STATUS property of RFC 5545 section 3.8.1.11.
func decodeStatus(value string) provider.Status {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "TENTATIVE":
		return provider.StatusTentative
	case "CANCELLED":
		return provider.StatusCancelled
	default:
		return provider.StatusConfirmed
	}
}

// encodeStatus writes the STATUS property.
func encodeStatus(s provider.Status) string {
	switch s {
	case provider.StatusTentative:
		return "TENTATIVE"
	case provider.StatusCancelled:
		return "CANCELLED"
	case provider.StatusConfirmed:
		return "CONFIRMED"
	default:
		return "CONFIRMED"
	}
}

// decodeTransparency maps the TRANSP property of RFC 5545 section
// 3.8.2.7.
func decodeTransparency(value string) provider.Transparency {
	if strings.EqualFold(strings.TrimSpace(value), "TRANSPARENT") {
		return provider.TransparencyTransparent
	}

	return provider.TransparencyOpaque
}

// encodeTransparency writes the TRANSP property.
func encodeTransparency(t provider.Transparency) string {
	if t == provider.TransparencyTransparent {
		return "TRANSPARENT"
	}

	return "OPAQUE"
}

// decodeVisibility maps the CLASS property of RFC 5545 section 3.8.1.3.
func decodeVisibility(value string) provider.Visibility {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "PUBLIC":
		return provider.VisibilityPublic
	case "PRIVATE":
		return provider.VisibilityPrivate
	case "CONFIDENTIAL":
		return provider.VisibilityConfidential
	default:
		return provider.VisibilityDefault
	}
}

// encodeVisibility writes the CLASS property, and returns an empty
// string where the calendar's own setting decides.
func encodeVisibility(v provider.Visibility) string {
	switch v {
	case provider.VisibilityPublic:
		return "PUBLIC"
	case provider.VisibilityPrivate:
		return "PRIVATE"
	case provider.VisibilityConfidential:
		return "CONFIDENTIAL"
	case provider.VisibilityDefault:
		return ""
	default:
		return ""
	}
}

// decodeAttendees maps the ATTENDEE properties of RFC 5545 section
// 3.8.4.1.
func decodeAttendees(component *icalComponent) []provider.Attendee {
	lines := component.props("ATTENDEE")
	if len(lines) == 0 {
		return nil
	}

	out := make([]provider.Attendee, 0, len(lines))
	for _, line := range lines {
		email := mailbox(line.Value)
		if email == "" {
			continue
		}
		out = append(out, provider.Attendee{
			Email:    email,
			Name:     line.param("CN"),
			Optional: strings.EqualFold(line.param("ROLE"), "OPT-PARTICIPANT"),
			Response: decodeResponse(line.param("PARTSTAT")),
		})
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// decodeOrganizer maps the ORGANIZER property of RFC 5545 section
// 3.8.4.3.
func decodeOrganizer(component *icalComponent) *provider.Attendee {
	line, ok := component.prop("ORGANIZER")
	if !ok {
		return nil
	}
	email := mailbox(line.Value)
	if email == "" {
		return nil
	}

	return &provider.Attendee{
		Email:    email,
		Name:     line.param("CN"),
		Response: decodeResponse(line.param("PARTSTAT")),
	}
}

// mailbox reads the address out of a CAL-ADDRESS value, which RFC 5545
// section 3.3.3 makes a URI, in practice a mailto one.
func mailbox(value string) string {
	trimmed := strings.TrimSpace(value)
	if at := strings.Index(strings.ToLower(trimmed), "mailto:"); at == 0 {
		return trimmed[len("mailto:"):]
	}
	if strings.Contains(trimmed, "@") && !strings.Contains(trimmed, ":") {
		return trimmed
	}

	return ""
}

// decodeResponse maps the PARTSTAT parameter of RFC 5545 section 3.2.12.
func decodeResponse(value string) provider.Response {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "ACCEPTED":
		return provider.ResponseAccepted
	case "DECLINED":
		return provider.ResponseDeclined
	case "TENTATIVE":
		return provider.ResponseTentative
	default:
		return provider.ResponseNeedsAction
	}
}

// encodeResponse writes the PARTSTAT parameter.
func encodeResponse(r provider.Response) string {
	switch r {
	case provider.ResponseAccepted:
		return "ACCEPTED"
	case provider.ResponseDeclined:
		return "DECLINED"
	case provider.ResponseTentative:
		return "TENTATIVE"
	case provider.ResponseNeedsAction:
		return "NEEDS-ACTION"
	default:
		return "NEEDS-ACTION"
	}
}

// decodeTimestamp reads a UTC date-time property, and returns the zero
// time where the server sent none.
func decodeTimestamp(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405"} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t.UTC()
		}
	}

	return time.Time{}
}

// createdStamp renders a UTC date-time property value, and is empty
// where the moment was never set.
func createdStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.UTC().Format("20060102T150405Z")
}

// encodeResource writes one event out as a complete calendar object
// resource, with a VTIMEZONE for every zone it names, as RFC 5545
// section 3.6.5 requires.
func encodeResource(event provider.Event, stamp time.Time, caps provider.Capabilities) (string, error) {
	if err := event.Validate(); err != nil {
		return "", err
	}

	var w icalWriter
	w.begin("VCALENDAR")
	w.raw("VERSION", nil, "2.0")
	w.raw("PRODID", nil, productID)
	w.raw("CALSCALE", nil, "GREGORIAN")

	for _, zone := range zonesOf(event) {
		block, err := vtimezone(zone, event.Start.Wall.Year())
		if err != nil {
			return "", err
		}
		w.out.WriteString(block)
	}

	w.begin("VEVENT")
	w.raw("UID", nil, event.UID)
	w.raw("DTSTAMP", nil, stamp.UTC().Format("20060102T150405Z"))
	if created := createdStamp(event.Created); created != "" {
		w.raw("CREATED", nil, created)
	}
	if updated := createdStamp(event.Updated); updated != "" {
		w.raw("LAST-MODIFIED", nil, updated)
	}
	writeEventTime(&w, "DTSTART", event.Start)
	writeEventTime(&w, "DTEND", event.End)
	if event.Summary != "" {
		w.text("SUMMARY", event.Summary)
	}
	if event.Description != "" {
		w.text("DESCRIPTION", event.Description)
	}
	if event.Location != "" {
		w.text("LOCATION", event.Location)
	}
	w.raw("STATUS", nil, encodeStatus(event.Status))
	w.raw("TRANSP", nil, encodeTransparency(event.Transparency))
	if class := encodeVisibility(event.Visibility); class != "" {
		w.raw("CLASS", nil, class)
	}
	for _, line := range event.Recurrence.Rule {
		w.out.WriteString(fold(line))
	}
	if caps.Attendees {
		for _, a := range event.Attendees {
			w.raw("ATTENDEE", attendeeParams(a), "mailto:"+a.Email)
		}
	}
	if event.Marker.Edge != "" {
		w.text(markerEdgeProp, event.Marker.Edge)
	}
	if event.Marker.Source != "" {
		w.text(markerSourceProp, event.Marker.Source)
	}
	w.end("VEVENT")
	w.end("VCALENDAR")

	return w.String(), nil
}

// attendeeParams builds the parameters one ATTENDEE line carries.
func attendeeParams(a provider.Attendee) []string {
	params := []string{"PARTSTAT=" + encodeResponse(a.Response)}
	if a.Name != "" {
		params = append(params, "CN="+quoteParam(a.Name))
	}
	if a.Optional {
		params = append(params, "ROLE=OPT-PARTICIPANT")
	} else {
		params = append(params, "ROLE=REQ-PARTICIPANT")
	}

	return params
}

// zonesOf returns the IANA zones the event names, in a stable order and
// without repeats.
func zonesOf(event provider.Event) []string {
	var out []string
	for _, t := range []provider.EventTime{event.Start, event.End} {
		if t.Kind != provider.TimeKindZoned || t.Zone == "" {
			continue
		}
		if !containsString(out, t.Zone) {
			out = append(out, t.Zone)
		}
	}

	return out
}

// containsString reports whether the slice already holds the value.
func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}

	return false
}

// writeEventTime writes one endpoint in the form its kind calls for.
func writeEventTime(w *icalWriter, name string, t provider.EventTime) {
	switch t.Kind {
	case provider.TimeKindDate:
		w.raw(name, []string{"VALUE=DATE"}, t.Wall.Format("20060102"))
	case provider.TimeKindZoned:
		if t.Zone == "UTC" {
			w.raw(name, nil, t.Wall.Format("20060102T150405")+"Z")

			return
		}
		w.raw(name, []string{"TZID=" + t.Zone}, t.Wall.Format("20060102T150405"))
	case provider.TimeKindFloating:
		w.raw(name, nil, t.Wall.Format("20060102T150405"))
	case provider.TimeKindUnset:
	}
}
