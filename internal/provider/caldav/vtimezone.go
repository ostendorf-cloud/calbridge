// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"fmt"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// RFC 5545 section 3.6.5 requires an iCalendar object to carry one
// VTIMEZONE for every TZID it names, and a server is free to read a bare
// TZID as UTC without one. This file builds that component out of the Go
// time zone database, which is the same tz database the zone names come
// from.
//
// A zone whose offsets change on a stable yearly rule gets the two
// observances and the yearly RRULE of the RFC's own example, and
// describes the zone without end. A zone with no change at all gets one
// standard observance, and does the same. A zone whose changes follow no
// yearly rule, such as Africa/Casablanca, can only be written out
// transition by transition, and the component then stops describing the
// zone after the last one it carries. vtimezone returns that horizon so
// the caller refuses an event reaching past it rather than writing one
// that is read against the wrong offset.

// windowYears is how far ahead the generator looks for transitions. It
// only bounds the written out form: a zone on a stable rule gets an
// open-ended RRULE instead.
const windowYears = 10

// vtimezone returns the VTIMEZONE component for an IANA zone, covering
// the given year and the years after it, and the moment past which the
// component no longer describes the zone.
//
// That second value is the zero time where the description holds
// without end: a zone that never changes, and a zone whose changes all
// follow a yearly rule, are both written with no horizon. A zone whose
// changes follow no such rule is written out transition by transition,
// and past the last one the component asserts the last offset forever,
// which is wrong rather than merely incomplete. The caller is told where
// that starts so it can refuse the write instead of storing an event
// that reads an hour out.
func vtimezone(zone string, year int) (component string, covered time.Time, err error) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return "", time.Time{}, fmt.Errorf(
			"time zone %q is not in the zone database: %w", zone, provider.ErrInvalidEvent)
	}

	from := time.Date(year-1, time.January, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(year+windowYears, time.December, 31, 23, 59, 59, 0, time.UTC)
	transitions := transitionsIn(location, from, until)

	var w icalWriter
	w.begin("VTIMEZONE")
	w.raw("TZID", nil, zone)
	if len(transitions) == 0 {
		writeFixedObservance(&w, location, from)
		w.end("VTIMEZONE")

		return w.String(), time.Time{}, w.err
	}

	horizon := time.Time{}
	for _, group := range groupTransitions(transitions) {
		ruled := writeObservance(&w, group)
		if ruled {
			continue
		}
		// The observance is written out rather than ruled, so it stops
		// describing the zone after its last onset. The earliest such
		// horizon is the one that binds.
		last := group.local[len(group.local)-1]
		if horizon.IsZero() || last.Before(horizon) {
			horizon = last
		}
	}
	w.end("VTIMEZONE")

	return w.String(), horizon, w.err
}

// transition is one change of offset: when it happens, what the offset
// was, what it becomes, and what the zone calls itself afterwards.
type transition struct {
	at         time.Time
	offsetFrom int
	offsetTo   int
	name       string
}

// isDaylight reports whether the transition moves the clock forward,
// which is what makes the observance a DAYLIGHT one.
func (t transition) isDaylight() bool { return t.offsetTo > t.offsetFrom }

// transitionsIn finds every offset change in the window, to the second.
// It steps a day at a time and narrows a day that changed, because the
// standard library exposes no list of a zone's transitions.
func transitionsIn(location *time.Location, from, until time.Time) []transition {
	var out []transition
	previous := from
	_, offset := previous.In(location).Zone()

	for cursor := from; cursor.Before(until); cursor = cursor.AddDate(0, 0, 1) {
		next := cursor.AddDate(0, 0, 1)
		_, nextOffset := next.In(location).Zone()
		if nextOffset == offset {
			previous = next

			continue
		}
		at := narrowTransition(location, previous, next, offset)
		name, _ := at.In(location).Zone()
		out = append(out, transition{at: at, offsetFrom: offset, offsetTo: nextOffset, name: name})
		offset, previous = nextOffset, next
	}

	return out
}

// narrowTransition binary searches for the first second at which the
// offset differs from before.
func narrowTransition(location *time.Location, before, after time.Time, offset int) time.Time {
	for after.Sub(before) > time.Second {
		middle := before.Add(after.Sub(before) / 2)
		if _, o := middle.In(location).Zone(); o == offset {
			before = middle
		} else {
			after = middle
		}
	}

	return after.Truncate(time.Second)
}

// observance is one STANDARD or DAYLIGHT block: the transitions of one
// kind, which either share a yearly rule or are written out.
type observance struct {
	daylight   bool
	offsetFrom int
	offsetTo   int
	name       string
	// local is the onset in local time, in the offset before the change,
	// one entry per transition.
	local []time.Time
}

// groupTransitions splits the transitions into the standard and the
// daylight observance, keeping the order the two first appear in.
func groupTransitions(transitions []transition) []observance {
	var out []observance
	index := map[bool]int{}
	for _, t := range transitions {
		local := t.at.Add(time.Duration(t.offsetFrom) * time.Second).UTC()
		at, seen := index[t.isDaylight()]
		if !seen {
			index[t.isDaylight()] = len(out)
			out = append(out, observance{
				daylight:   t.isDaylight(),
				offsetFrom: t.offsetFrom,
				offsetTo:   t.offsetTo,
				name:       t.name,
				local:      []time.Time{local},
			})

			continue
		}
		out[at].local = append(out[at].local, local)
	}

	return out
}

// writeObservance writes one STANDARD or DAYLIGHT block, and reports
// whether its onsets were described by a yearly rule rather than written
// out one by one.
func writeObservance(w *icalWriter, o observance) (ruled bool) {
	name := "STANDARD"
	if o.daylight {
		name = "DAYLIGHT"
	}
	w.begin(name)
	w.raw("DTSTART", nil, o.local[0].Format("20060102T150405"))
	w.raw("TZOFFSETFROM", nil, utcOffset(o.offsetFrom))
	w.raw("TZOFFSETTO", nil, utcOffset(o.offsetTo))
	if o.name != "" {
		w.raw("TZNAME", nil, o.name)
	}
	rule, ruled := yearlyRule(o.local)
	if ruled {
		w.raw("RRULE", nil, rule)
	} else {
		for _, at := range o.local[1:] {
			w.raw("RDATE", nil, at.Format("20060102T150405"))
		}
	}
	w.end(name)

	return ruled
}

// writeFixedObservance writes the single block a zone with no change at
// all needs.
func writeFixedObservance(w *icalWriter, location *time.Location, at time.Time) {
	name, offset := at.In(location).Zone()
	w.begin("STANDARD")
	w.raw("DTSTART", nil, "19700101T000000")
	w.raw("TZOFFSETFROM", nil, utcOffset(offset))
	w.raw("TZOFFSETTO", nil, utcOffset(offset))
	if name != "" {
		w.raw("TZNAME", nil, name)
	}
	w.end("STANDARD")
}

// weekdayNames are the two letter day names RFC 5545 section 3.3.10
// uses in a BYDAY part.
var weekdayNames = [...]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

// yearlyRule returns the FREQ=YEARLY rule the onsets all follow, and
// whether they follow one at all. They do when every onset falls in the
// same month, on the same weekday, at the same position in that month
// and at the same local time, which is the shape the RFC's own example
// has.
func yearlyRule(onsets []time.Time) (string, bool) {
	if len(onsets) < 2 {
		return "", false
	}

	month, weekday, position := onsets[0].Month(), onsets[0].Weekday(), weekPosition(onsets[0])
	hour, minute, second := onsets[0].Clock()
	year := onsets[0].Year()
	for _, at := range onsets[1:] {
		h, m, s := at.Clock()
		if at.Month() != month || at.Weekday() != weekday || weekPosition(at) != position {
			return "", false
		}
		if h != hour || m != minute || s != second {
			return "", false
		}
		year++
		if at.Year() != year {
			return "", false
		}
	}

	return fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", int(month), position, weekdayNames[weekday]), true
}

// weekPosition returns which occurrence of its weekday in the month the
// date is, counted from the front, or -1 where it is the last one.
func weekPosition(at time.Time) int {
	if at.AddDate(0, 0, 7).Month() != at.Month() {
		return -1
	}

	return (at.Day()-1)/7 + 1
}

// utcOffset renders an offset in the form RFC 5545 section 3.3.14 gives
// TZOFFSETFROM and TZOFFSETTO.
func utcOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainder := seconds % 60

	out := fmt.Sprintf("%s%02d%02d", sign, hours, minutes)
	if remainder != 0 {
		out += fmt.Sprintf("%02d", remainder)
	}

	return out
}
