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

// rule is the part of RFC 5545's recurrence grammar this package
// expands. Everything outside it is refused by parseRule, because an
// approximated series is worse in a calendar than a reported failure.
type rule struct {
	freq     frequency
	interval int
	count    int
	until    time.Time
	byDay    []time.Weekday
	exDates  map[string]struct{}
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
	parsed := rule{interval: 1, exDates: map[string]struct{}{}}
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
		stamp, err := parseStamp(strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		r.exDates[stampKey(stamp)] = struct{}{}
	}

	return nil
}

// parseStamp reads an RFC 5545 date or date-time as a civil time. A
// trailing Z marks UTC in the source, and the civil reading is what the
// expander compares, so it is read and dropped.
func parseStamp(value string) (time.Time, error) {
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		stamp, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			return stamp, nil
		}
	}

	return time.Time{}, fmt.Errorf("date %q: %w", value, ErrUnsupportedRecurrence)
}

// stampKey renders a civil time as the key EXDATE matching uses. The
// date alone is deliberately not enough: an EXDATE naming a date-time
// excludes that occurrence and not the whole day.
func stampKey(t time.Time) string { return t.Format("20060102T150405") }

// occurrences expands the rule from start, and returns the occurrences
// falling inside [from, to]. Each carries the kind and zone start
// carries, because a rule repeats a wall clock reading, not an instant.
func (r rule) occurrences(start provider.EventTime, from, to time.Time) ([]provider.EventTime, error) {
	out := []provider.EventTime{}
	generated := 0

	for i := range maxCandidates {
		candidate, ok := r.candidate(start.Wall, i)
		if !ok {
			continue
		}
		if !r.until.IsZero() && candidate.After(r.until) {
			break
		}

		generated++
		if r.count > 0 && generated > r.count {
			break
		}
		if candidate.After(to) {
			break
		}

		if _, excluded := r.exDates[stampKey(candidate)]; excluded || candidate.Before(from) {
			continue
		}

		out = append(out, provider.EventTime{Kind: start.Kind, Wall: candidate, Zone: start.Zone})
		if len(out) > maxOccurrences {
			return nil, fmt.Errorf("more than %d occurrences in the window: %w", maxOccurrences, ErrRecurrenceTooLarge)
		}
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
