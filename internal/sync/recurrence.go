// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// Window is the span a repeating event is expanded into when the target
// cannot hold the rule itself. It is rolling: every pass measures it from
// the moment the pass runs, so the series keeps extending forward and
// stops carrying occurrences nobody looks at any more.
type Window struct {
	// Past is how far back of the current moment an occurrence is still
	// copied.
	Past time.Duration
	// Future is how far ahead of the current moment occurrences are
	// copied.
	Future time.Duration
}

// DefaultWindow keeps a month of history, which is as far back as a
// calendar is read in practice, and a year ahead, which covers an annual
// series without expanding one forever.
var DefaultWindow = Window{Past: 30 * 24 * time.Hour, Future: 365 * 24 * time.Hour}

// Valid reports whether the window spans anything at all.
func (w Window) Valid() bool { return w.Past >= 0 && w.Future > 0 }

// bounds returns the first and last moment the window covers around now.
func (w Window) bounds(now time.Time) (from, to time.Time) {
	return now.Add(-w.Past), now.Add(w.Future)
}

// maxOccurrences bounds one expansion. A rule producing more than this
// inside the window is reported rather than written: an edge that creates
// several thousand events in one pass is a rule nobody meant to write.
const maxOccurrences = 1000

// maxCandidates bounds the generator itself, so a rule whose occurrences
// all fall outside the window still terminates.
const maxCandidates = 100000

// frequency is the unit a rule repeats on.
type frequency uint8

const (
	freqUnset frequency = iota
	freqDaily
	freqWeekly
	freqMonthly
	freqYearly
)

// stamp is a date or date-time read out of a recurrence line, together
// with whether it was written as an absolute moment. RFC 5545 spells an
// UNTIL or an EXDATE of a zoned series in UTC, with a trailing Z, while
// the occurrences themselves are wall clock readings in the series' own
// zone. The two are only comparable once one has been converted into the
// other, and dropping the Z silently compares a Berlin reading against a
// UTC one.
type stamp struct {
	at time.Time
	// utc reports that the value carried a trailing Z, so at is an
	// absolute moment rather than a wall clock reading.
	utc bool
}

// rule is the part of RFC 5545's recurrence grammar this package
// expands. Everything outside it is refused by parseRule, because an
// approximated series is worse in a calendar than a reported failure.
type rule struct {
	freq     frequency
	interval int
	count    int
	until    stamp
	byDay    []time.Weekday
	exDates  []stamp
}

// weekdays maps RFC 5545's two letter day names.
var weekdays = map[string]time.Weekday{
	"SU": time.Sunday,
	"MO": time.Monday,
	"TU": time.Tuesday,
	"WE": time.Wednesday,
	"TH": time.Thursday,
	"FR": time.Friday,
	"SA": time.Saturday,
}

// parseRule reads the recurrence lines of a repeating master. It accepts
// one RRULE and any number of EXDATE lines, and refuses everything else,
// so a series the expander would get wrong is never written at all.
func parseRule(lines []string) (rule, error) {
	parsed := rule{interval: 1}
	seenRule := false

	for _, line := range lines {
		name, value, found := strings.Cut(line, ":")
		if !found {
			return rule{}, fmt.Errorf("recurrence line %q has no value: %w", line, ErrUnsupportedRecurrence)
		}

		switch strings.ToUpper(strings.TrimSpace(name)) {
		case "RRULE":
			if seenRule {
				return rule{}, fmt.Errorf("more than one RRULE: %w", ErrUnsupportedRecurrence)
			}
			seenRule = true
			if err := parsed.readRule(value); err != nil {
				return rule{}, err
			}
		case "EXDATE":
			if err := parsed.readExDates(value); err != nil {
				return rule{}, err
			}
		default:
			return rule{}, fmt.Errorf("recurrence line %q: %w", name, ErrUnsupportedRecurrence)
		}
	}

	if !seenRule {
		return rule{}, fmt.Errorf("no RRULE: %w", ErrUnsupportedRecurrence)
	}

	return parsed, nil
}

// readRule reads one RRULE's parts.
func (r *rule) readRule(value string) error {
	for _, part := range strings.Split(value, ";") {
		if part == "" {
			continue
		}
		key, raw, found := strings.Cut(part, "=")
		if !found {
			return fmt.Errorf("rule part %q has no value: %w", part, ErrUnsupportedRecurrence)
		}

		if err := r.readPart(strings.ToUpper(strings.TrimSpace(key)), strings.ToUpper(strings.TrimSpace(raw))); err != nil {
			return err
		}
	}

	if r.freq == freqUnset {
		return fmt.Errorf("rule names no FREQ: %w", ErrUnsupportedRecurrence)
	}
	if len(r.byDay) > 0 && r.freq != freqWeekly {
		return fmt.Errorf("BYDAY outside a weekly rule: %w", ErrUnsupportedRecurrence)
	}

	return nil
}

// readPart reads one key and value of an RRULE.
func (r *rule) readPart(key, value string) error {
	switch key {
	case "FREQ":
		return r.readFreq(value)
	case "INTERVAL":
		interval, err := strconv.Atoi(value)
		if err != nil || interval < 1 {
			return fmt.Errorf("INTERVAL %q: %w", value, ErrUnsupportedRecurrence)
		}
		r.interval = interval
	case "COUNT":
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 {
			return fmt.Errorf("COUNT %q: %w", value, ErrUnsupportedRecurrence)
		}
		r.count = count
	case "UNTIL":
		until, err := parseStamp(value)
		if err != nil {
			return err
		}
		r.until = until
	case "BYDAY":
		return r.readByDay(value)
	case "WKST":
		// The week start only changes a weekly rule that repeats every
		// other week or wider. Monday is what this expander counts weeks
		// from, so any other week start would shift those occurrences.
		if value != "MO" {
			return fmt.Errorf("WKST %q: %w", value, ErrUnsupportedRecurrence)
		}
	default:
		return fmt.Errorf("rule part %q: %w", key, ErrUnsupportedRecurrence)
	}

	return nil
}

// readFreq reads the FREQ part.
func (r *rule) readFreq(value string) error {
	switch value {
	case "DAILY":
		r.freq = freqDaily
	case "WEEKLY":
		r.freq = freqWeekly
	case "MONTHLY":
		r.freq = freqMonthly
	case "YEARLY":
		r.freq = freqYearly
	default:
		return fmt.Errorf("FREQ %q: %w", value, ErrUnsupportedRecurrence)
	}

	return nil
}

// readByDay reads the BYDAY part, which this expander supports as plain
// day names. An ordinal such as "2TH", the second Thursday of the month,
// belongs to a monthly rule and is refused with it.
func (r *rule) readByDay(value string) error {
	for _, name := range strings.Split(value, ",") {
		day, ok := weekdays[strings.TrimSpace(name)]
		if !ok {
			return fmt.Errorf("BYDAY %q: %w", name, ErrUnsupportedRecurrence)
		}
		if !slices.Contains(r.byDay, day) {
			r.byDay = append(r.byDay, day)
		}
	}
	slices.Sort(r.byDay)

	return nil
}

// readExDates reads one EXDATE line, which excludes the occurrences it
// names from the series.
func (r *rule) readExDates(value string) error {
	for _, raw := range strings.Split(value, ",") {
		excluded, err := parseStamp(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		r.exDates = append(r.exDates, excluded)
	}

	return nil
}

// parseStamp reads an RFC 5545 date or date-time, keeping whether it was
// written as an absolute moment. A value with a trailing Z is parsed in
// UTC and reported as absolute; one without is a wall clock reading in
// the series' own zone. Which it is decides what it may be compared
// against, so the Z is never dropped.
func parseStamp(value string) (stamp, error) {
	layouts := []struct {
		layout string
		utc    bool
	}{
		{"20060102T150405Z", true},
		{"20060102T150405", false},
		{"20060102", false},
	}

	for _, candidate := range layouts {
		at, err := time.ParseInLocation(candidate.layout, value, time.UTC)
		if err == nil {
			return stamp{at: at, utc: candidate.utc}, nil
		}
	}

	return stamp{}, fmt.Errorf("date %q: %w", value, ErrUnsupportedRecurrence)
}

// wallReading converts an absolute moment into the wall clock reading the
// series' own zone shows at it. A series that is not zoned, an all-day or
// a floating one, has no zone to read it in, so the value stands as it is.
func wallReading(at time.Time, start provider.EventTime) (time.Time, error) {
	if start.Kind != provider.TimeKindZoned {
		return at, nil
	}

	loc, err := time.LoadLocation(start.Zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load zone %q: %w", start.Zone, err)
	}
	local := at.UTC().In(loc)

	return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.UTC), nil
}

// windowIn converts the rolling window, two absolute moments, into the
// wall clock readings the series' own zone shows at them. The
// occurrences a rule produces are wall readings, so a window left as an
// instant would cut the series at the event's own offset from UTC.
func windowIn(from, to time.Time, start provider.EventTime) (wallFrom, wallTo time.Time, err error) {
	wallFrom, err = wallReading(from, start)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	wallTo, err = wallReading(to, start)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	return wallFrom, wallTo, nil
}

// limits are a rule's own bounds, resolved into the wall clock readings
// the series is expanded in, so every comparison during the expansion is
// between two readings of the same clock.
type limits struct {
	until   time.Time
	exDates map[string]struct{}
}

// limitsFor resolves UNTIL and every EXDATE into the series' own
// reading. A value written as an absolute moment is converted; one
// written as a wall reading already is one.
func (r rule) limitsFor(start provider.EventTime) (limits, error) {
	resolved := limits{exDates: make(map[string]struct{}, len(r.exDates))}

	if !r.until.at.IsZero() {
		until, err := r.until.reading(start)
		if err != nil {
			return limits{}, fmt.Errorf("UNTIL: %w", err)
		}
		resolved.until = until
	}

	for _, excluded := range r.exDates {
		at, err := excluded.reading(start)
		if err != nil {
			return limits{}, fmt.Errorf("EXDATE: %w", err)
		}
		resolved.exDates[stampKey(at)] = struct{}{}
	}

	return resolved, nil
}

// reading returns the stamp as a wall clock reading in the series' zone.
func (s stamp) reading(start provider.EventTime) (time.Time, error) {
	if !s.utc {
		return s.at, nil
	}

	return wallReading(s.at, start)
}

// stampKey renders a wall clock reading as the key EXDATE matching uses.
// The date alone is deliberately not enough: an EXDATE naming a
// date-time excludes that occurrence and not the whole day.
func stampKey(t time.Time) string { return t.Format(occurrenceLayout) }

// occurrences expands the rule from start, and returns the occurrences
// falling inside [from, to]. Each carries the kind and zone start
// carries, because a rule repeats a wall clock reading, not an instant.
//
// from and to are wall clock readings in the series' own zone, which is
// what windowIn turns the rolling window into. UNTIL and EXDATE are
// resolved into the same reading here, so no comparison below crosses
// two clocks.
func (r rule) occurrences(start provider.EventTime, from, to time.Time) ([]provider.EventTime, error) {
	bounds, err := r.limitsFor(start)
	if err != nil {
		return nil, err
	}

	out := []provider.EventTime{}
	generated := 0
	reachedEnd := false

	for i := range maxCandidates {
		candidate, ok := r.candidate(start.Wall, i)
		if !ok {
			continue
		}
		if !bounds.until.IsZero() && candidate.After(bounds.until) {
			reachedEnd = true

			break
		}

		generated++
		if r.count > 0 && generated > r.count {
			reachedEnd = true

			break
		}
		if candidate.After(to) {
			reachedEnd = true

			break
		}

		if _, excluded := bounds.exDates[stampKey(candidate)]; excluded || candidate.Before(from) {
			continue
		}

		out = append(out, provider.EventTime{Kind: start.Kind, Wall: candidate, Zone: start.Zone})
		if len(out) > maxOccurrences {
			return nil, fmt.Errorf("more than %d occurrences in the window: %w", maxOccurrences, ErrRecurrenceTooLarge)
		}
	}

	// Running out of candidates means the generator never reached the end
	// of the window, which is a rule starting far enough in the past that
	// this expander cannot carry it. Returning what was found would copy
	// a truncated series and call it complete.
	if !reachedEnd {
		return nil, fmt.Errorf("the rule needs more than %d candidates to reach the window: %w", maxCandidates, ErrUnsupportedRecurrence)
	}

	return out, nil
}

// candidate returns the nth occurrence the rule generates from start, and
// whether the rule generates one at all at that position: a monthly rule
// on the 31st skips the months that are shorter, rather than sliding into
// the next one.
func (r rule) candidate(start time.Time, n int) (time.Time, bool) {
	switch r.freq {
	case freqDaily:
		return start.AddDate(0, 0, n*r.interval), true
	case freqWeekly:
		return r.weeklyCandidate(start, n)
	case freqMonthly:
		return keepDay(start, start.AddDate(0, n*r.interval, 0))
	case freqYearly:
		return keepDay(start, start.AddDate(n*r.interval, 0, 0))
	case freqUnset:
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// weeklyCandidate returns the nth occurrence of a weekly rule. Without
// BYDAY the series repeats start's own weekday. With it, each counted
// week contributes one occurrence per named day, in week order.
func (r rule) weeklyCandidate(start time.Time, n int) (time.Time, bool) {
	if len(r.byDay) == 0 {
		return start.AddDate(0, 0, n*7*r.interval), true
	}

	week, within := n/len(r.byDay), n%len(r.byDay)
	monday := start.AddDate(0, 0, -daysSinceMonday(start.Weekday())+week*7*r.interval)
	candidate := monday.AddDate(0, 0, daysSinceMonday(r.byDay[within]))

	// The first counted week starts before the event does whenever
	// DTSTART is not that week's first named day.
	if candidate.Before(start) {
		return time.Time{}, false
	}

	return candidate, true
}

// daysSinceMonday is a weekday's offset in a week starting on Monday.
func daysSinceMonday(day time.Weekday) int { return (int(day) + 6) % 7 }

// keepDay reports a generated date only where it still falls on the day
// of the month the series started on. time.AddDate normalises the 31st of
// a month into the next one, which RFC 5545 does not do: that month has
// no occurrence at all.
func keepDay(start, candidate time.Time) (time.Time, bool) {
	if candidate.Day() != start.Day() {
		return time.Time{}, false
	}

	return candidate, true
}
