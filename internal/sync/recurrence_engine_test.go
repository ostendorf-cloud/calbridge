// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// weekly is a repeating master in the source calendar.
func weekly(rule ...string) provider.Event {
	event := meeting("Jour fixe", 4, 9)
	event.Recurrence = provider.Recurrence{Rule: rule}

	return event
}

// shortWindow keeps the expansion small enough to read in a failure
// message: four weeks ahead of testNow.
var shortWindow = sync.Window{Past: 24 * time.Hour, Future: 28 * 24 * time.Hour}

func TestEngine_Recurrence_copiesTheRuleWhereTheTargetHoldsOne(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{
		targetCaps: provider.Capabilities{CalendarCreation: true, Attendees: true, NativeRecurrence: true},
		window:     shortWindow,
	})
	source := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=4"))

	wantActions(t, h.apply(source), "create/none")

	copied := h.onlyCopy()
	if len(copied.Recurrence.Rule) != 1 || copied.Recurrence.Rule[0] != "RRULE:FREQ=WEEKLY;COUNT=4" {
		t.Fatalf("the copy does not carry the source's rule: %+v", copied.Recurrence)
	}
}

func TestEngine_Recurrence_expandsWhereTheTargetHoldsNoRule(t *testing.T) {
	t.Parallel()

	// The target declares no native recurrence, so the series is carried
	// as one event per occurrence. The capability declaration decides, not
	// the provider's name.
	h := newHarness(t, setup{window: shortWindow})
	source := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=4"))

	wantActions(t, h.apply(source), "create/none", "create/none", "create/none", "create/none")

	copies := h.copies()
	if len(copies) != 4 {
		t.Fatalf("copies: got %d, want 4", len(copies))
	}
	for _, copied := range copies {
		if len(copied.Recurrence.Rule) != 0 {
			t.Fatalf("an expanded occurrence carries a rule: %+v", copied.Recurrence)
		}
		if copied.End.Wall.Sub(copied.Start.Wall) != time.Hour {
			t.Fatalf("occurrence %q lasts %s, want an hour", copied.ID, copied.End.Wall.Sub(copied.Start.Wall))
		}
	}

	mappings := h.store.mappingsOf(h.binding.Edge.ID)
	if len(mappings) != 4 {
		t.Fatalf("mapping rows: got %d, want 4", len(mappings))
	}
	if mappings[0].SourceEventID != source.ID+"#20260304T090000" {
		t.Fatalf("the first occurrence is mapped under %q", mappings[0].SourceEventID)
	}
}

func TestEngine_Recurrence_expandsWhenTheMaskDropsTheRule(t *testing.T) {
	t.Parallel()

	// The target could hold the rule. The edge copies a busy block, which
	// does not include recurrence, so the series is expanded anyway.
	h := newHarness(t, setup{
		targetCaps: provider.Capabilities{CalendarCreation: true, Attendees: true, NativeRecurrence: true},
		level:      storage.DetailLevelBusyBlock,
		window:     shortWindow,
	})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))

	wantActions(t, h.reconcile(), "create/none", "create/none", "create/none")
}

func TestEngine_Recurrence_writesNothingOnASecondPass(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=4"))

	h.reconcile()
	wantActions(t, h.reconcile(), "none/unchanged", "none/unchanged", "none/unchanged", "none/unchanged")
	wantWrites(t, h.target, 4, 0, 0)
}

func TestEngine_Recurrence_removesAnOccurrenceTheRuleStoppedProducing(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{window: shortWindow})
	source := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=4"))
	h.reconcile()

	source.Recurrence.Rule = []string{"RRULE:FREQ=WEEKLY;COUNT=4", "EXDATE:20260311T090000"}
	h.put(source)

	report := h.reconcile()
	if got := report.Count(sync.ActionDelete); got != 1 {
		t.Fatalf("deletes: got %d, want 1 (all: %v)", got, actions(report))
	}
	if got := len(h.copies()); got != 3 {
		t.Fatalf("copies: got %d, want 3", got)
	}
}

func TestEngine_Recurrence_leavesNothingBehindWhenTheDetailLevelIsLowered(t *testing.T) {
	t.Parallel()

	// The target holds rules, so a full copy edge writes one master. The
	// busy block preset drops the recurrence field, which switches the
	// edge to expansion: the master's own key is never written again, and
	// the copy under it would otherwise stand in the target forever, at
	// the detail level the user just lowered away from.
	h := newHarness(t, setup{
		targetCaps: provider.Capabilities{CalendarCreation: true, Attendees: true, NativeRecurrence: true},
		level:      storage.DetailLevelFull,
		window:     shortWindow,
	})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	if got := len(h.copies()); got != 1 {
		t.Fatalf("copies at the full level: got %d, want 1", got)
	}

	h.setDetailLevel(storage.DetailLevelBusyBlock)

	report := h.reconcile()
	if got := report.CountReason(sync.ReasonStrategyChanged); got != 1 {
		t.Fatalf("strategy changes: got %d, want 1 (all: %v)", got, actions(report))
	}

	copies := h.copies()
	if len(copies) != 3 {
		t.Fatalf("copies after lowering the level: got %d, want 3", len(copies))
	}
	for _, copied := range copies {
		if copied.Summary != "" || copied.Description != "" || copied.Location != "" {
			t.Fatalf("a copy still carries detail a busy block drops: %+v", copied)
		}
		if len(copied.Attendees) != 0 || len(copied.Recurrence.Rule) != 0 {
			t.Fatalf("a copy still carries attendees or a rule: %+v", copied)
		}
	}

	// The repair is not a one-off: the next pass finds nothing left to do.
	wantActions(t, h.reconcile(), "none/unchanged", "none/unchanged", "none/unchanged")
}

func TestEngine_Recurrence_leavesNothingBehindWhenTheDetailLevelIsRaised(t *testing.T) {
	t.Parallel()

	// The same switch in the other direction: an edge that expanded the
	// series starts copying the rule, and the one copy per occurrence has
	// to go, or the target shows the series twice.
	h := newHarness(t, setup{
		targetCaps: provider.Capabilities{CalendarCreation: true, Attendees: true, NativeRecurrence: true},
		level:      storage.DetailLevelBusyBlock,
		window:     shortWindow,
	})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	if got := len(h.copies()); got != 3 {
		t.Fatalf("copies at the busy block level: got %d, want 3", got)
	}

	h.setDetailLevel(storage.DetailLevelFull)

	report := h.reconcile()
	if got := report.CountReason(sync.ReasonStrategyChanged); got != 3 {
		t.Fatalf("strategy changes: got %d, want 3 (all: %v)", got, actions(report))
	}

	copies := h.copies()
	if len(copies) != 1 {
		t.Fatalf("copies after raising the level: got %d, want 1", len(copies))
	}
	if len(copies[0].Recurrence.Rule) != 1 {
		t.Fatalf("the copy does not carry the rule: %+v", copies[0].Recurrence)
	}

	wantActions(t, h.reconcile(), "none/unchanged")
}

func TestEngine_Recurrence_reportsAnOccurrenceThatLeftTheWindow(t *testing.T) {
	t.Parallel()

	// An occurrence the window rolled past is not a deleted event, and an
	// operator reading the report has to be able to tell the two apart.
	h := newHarness(t, setup{window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=4"))
	h.reconcile()

	before := len(h.copies())
	if before == 0 {
		t.Fatal("the first pass copied nothing")
	}

	h.moveNow(testNow.AddDate(0, 0, 21))

	report := h.reconcile()
	if got := report.CountReason(sync.ReasonOutOfWindow); got == 0 {
		t.Fatalf("no occurrence reported as out of window (all: %v)", actions(report))
	}
	if got := report.CountReason(sync.ReasonSourceGone); got != 0 {
		t.Fatalf("an occurrence that left the window reads as a deleted source (all: %v)", actions(report))
	}
	if got := len(h.copies()); got >= before {
		t.Fatalf("copies after the window rolled: got %d, want fewer than %d", got, before)
	}
}

func TestEngine_Recurrence_usesTheSourcesOwnInstanceForAnOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{window: shortWindow})
	master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))

	moved := meeting("Jour fixe, verlegt", 11, 14)
	moved.Recurrence = provider.Recurrence{
		MasterID:      master.ID,
		OriginalStart: provider.Zoned(time.Date(2026, time.March, 11, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
	h.put(moved)

	h.reconcile()

	copies := h.copies()
	if len(copies) != 3 {
		t.Fatalf("copies: got %d, want 3", len(copies))
	}

	var found bool
	for _, copied := range copies {
		if copied.Summary == "Jour fixe, verlegt" {
			found = true
			if copied.Start.Wall.Hour() != 14 {
				t.Fatalf("the overridden occurrence starts at %s, want 14:00", copied.Start)
			}
		}
	}
	if !found {
		t.Fatal("the overridden occurrence was copied from the rule instead of from the instance")
	}
}

func TestEngine_Recurrence_skipsAnInstanceWhoseMasterIsCopied(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{
		targetCaps: provider.Capabilities{CalendarCreation: true, Attendees: true, NativeRecurrence: true},
		window:     shortWindow,
	})
	master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))

	moved := meeting("Jour fixe, verlegt", 11, 14)
	moved.Recurrence = provider.Recurrence{
		MasterID:      master.ID,
		OriginalStart: provider.Zoned(time.Date(2026, time.March, 11, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
	stored := h.put(moved)

	wantActions(t, h.apply(stored), "none/covered by master")
	wantWrites(t, h.target, 0, 0, 0)
}

func TestEngine_Recurrence_reportsARuleTheExpanderDoesNotImplement(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{window: shortWindow})
	source := h.put(weekly("RRULE:FREQ=MONTHLY;BYSETPOS=-1;BYDAY=FR"))

	_, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindUpsert,
		EventID: source.ID,
		Event:   source,
	})
	if !errors.Is(err, sync.ErrUnsupportedRecurrence) {
		t.Fatalf("got %v, want %v", err, sync.ErrUnsupportedRecurrence)
	}
	wantWrites(t, h.target, 0, 0, 0)
}

func TestEngine_Recurrence_removesEveryOccurrenceWhenTheMasterIsDeleted(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{window: shortWindow})
	source := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=4"))
	h.reconcile()

	report, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindDelete,
		EventID: source.ID,
	})
	if err != nil {
		t.Fatalf("apply delete: %v", err)
	}

	if got := report.Count(sync.ActionDelete); got != 4 {
		t.Fatalf("deletes: got %d, want 4 (all: %v)", got, actions(report))
	}
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies: got %d, want none", got)
	}
}

func TestEngine_Recurrence_followsTheWindowForward(t *testing.T) {
	t.Parallel()

	// The same rule, read a month later, carries a different stretch of
	// the series: the window rolls with the moment the pass runs.
	h := newHarness(t, setup{window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY"))
	h.reconcile()

	before := h.copies()
	if len(before) == 0 {
		t.Fatal("the first pass copied nothing")
	}

	later := newHarness(t, setup{window: shortWindow, now: testNow.AddDate(0, 2, 0)})
	later.put(weekly("RRULE:FREQ=WEEKLY"))
	later.reconcile()

	after := later.copies()
	if len(after) != len(before) {
		t.Fatalf("occurrences in the window: got %d two months later, want %d", len(after), len(before))
	}
	if after[0].Start.Wall.Before(testNow) {
		t.Fatalf("the later pass still copies %s, which is behind the window", after[0].Start)
	}
}
