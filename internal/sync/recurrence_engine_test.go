// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

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
	h.replace(source)

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

func TestEngine_Recurrence_removesTheOccurrencesWhenTheEventStopsRepeating(t *testing.T) {
	t.Parallel()

	// An event that loses its rule is not a repeating event any more, so
	// nothing that handles one runs for it. Its expanded copies sit under
	// keys the edge stopped writing to, and the event they name is still
	// in the calendar, so only the question "does this edge still write
	// here" removes them.
	h := newHarness(t, setup{window: shortWindow})
	master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	if got := len(h.copies()); got != 3 {
		t.Fatalf("copies while the event repeats: got %d, want 3", got)
	}

	master.Recurrence = provider.Recurrence{}
	h.replace(master)

	report := h.reconcile()
	if got := report.CountReason(sync.ReasonStrategyChanged); got != 3 {
		t.Fatalf("occurrence copies removed: got %d, want 3 (all: %v)", got, actions(report))
	}

	copies := h.copies()
	if len(copies) != 1 {
		t.Fatalf("copies after the rule was dropped: got %d, want 1", len(copies))
	}
	if copies[0].Marker.Source != master.ID {
		t.Fatalf("the surviving copy is mapped to %q, want the event's own id %q", copies[0].Marker.Source, master.ID)
	}
	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != 1 {
		t.Fatalf("mapping rows: got %d, want 1", got)
	}

	wantActions(t, h.reconcile(), "none/unchanged")
}

func TestEngine_Recurrence_removesTheOccurrencesFromASingleChange(t *testing.T) {
	t.Parallel()

	// The same repair on the path a push notification takes. A single
	// change sees one event and no calendar, so the sweep a full pass
	// does is not available: the event arriving without a rule is the
	// only evidence that its occurrence copies are stale, and it has to
	// be enough.
	h := newHarness(t, setup{window: shortWindow})
	master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))

	h.apply(master)
	assertCopies(t, h, 3)

	master.Recurrence = provider.Recurrence{}
	master = h.replace(master)

	report := h.apply(master)
	if got := report.CountReason(sync.ReasonStrategyChanged); got != 3 {
		t.Fatalf("occurrence copies removed: got %d, want 3 (all: %v)", got, actions(report))
	}

	copies := assertCopies(t, h, 1)
	if copies[0].Marker.Source != master.ID {
		t.Fatalf("the surviving copy is mapped to %q, want the event's own id %q", copies[0].Marker.Source, master.ID)
	}

	// Lowering the level afterwards is where a copy left behind would
	// show more than the user asked for.
	h.setDetailLevel(storage.DetailLevelBusyBlock)
	h.apply(master)

	for _, copied := range assertCopies(t, h, 1) {
		if copied.Summary != "" || copied.Description != "" || copied.Location != "" {
			t.Fatalf("a copy still carries detail a busy block drops: %+v", copied)
		}
	}

	wantActions(t, h.apply(master), "none/unchanged")
}

func TestEngine_Recurrence_leavesNothingAtTheOldDetailLevel(t *testing.T) {
	t.Parallel()

	// Three routes put a repeating event's copies under a key the edge
	// stops writing to: the mask dropping recurrence, the target losing
	// the capability to hold a rule, and the event itself losing its
	// rule. Each is followed by lowering the detail level, which is where
	// a copy left behind becomes a copy showing more than the user asked
	// for.
	tests := []struct {
		name         string
		nativeTarget bool
		wantFirst    int
		change       func(h *harness, master provider.Event)
		wantChanged  int
		wantLowered  int
	}{
		{
			// Nothing happens before the lowering here: dropping the
			// recurrence field is itself what switches the strategy.
			name:         "the mask stops copying the rule",
			nativeTarget: true,
			wantFirst:    1,
			change:       func(*harness, provider.Event) {},
			wantChanged:  1,
			wantLowered:  3,
		},
		{
			name:         "the target stops holding rules",
			nativeTarget: true,
			wantFirst:    1,
			change: func(h *harness, _ provider.Event) {
				h.target.SetCapabilities(provider.Capabilities{CalendarCreation: true, Attendees: true})
			},
			wantChanged: 3,
			wantLowered: 3,
		},
		{
			name:         "the event stops repeating",
			nativeTarget: false,
			wantFirst:    3,
			change: func(h *harness, master provider.Event) {
				master.Recurrence = provider.Recurrence{}
				h.replace(master)
			},
			wantChanged: 1,
			wantLowered: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caps := provider.Capabilities{CalendarCreation: true, Attendees: true, NativeRecurrence: tt.nativeTarget}
			h := newHarness(t, setup{targetCaps: caps, level: storage.DetailLevelFull, window: shortWindow})

			master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
			h.reconcile()
			assertCopies(t, h, tt.wantFirst)

			tt.change(h, master)
			h.reconcile()
			assertCopies(t, h, tt.wantChanged)

			h.setDetailLevel(storage.DetailLevelBusyBlock)
			h.reconcile()

			copies := assertCopies(t, h, tt.wantLowered)
			for _, copied := range copies {
				if copied.Summary != "" || copied.Description != "" || copied.Location != "" {
					t.Fatalf("a copy still carries detail a busy block drops: %+v", copied)
				}
				if len(copied.Attendees) != 0 {
					t.Fatalf("a copy still carries attendees: %+v", copied)
				}
			}

			// The repair settles: the pass after it writes nothing.
			for _, op := range h.reconcile().Operations {
				if op.Action != sync.ActionNone || op.Reason != sync.ReasonUnchanged {
					t.Fatalf("the pass after the repair is not settled: %s", op)
				}
			}
		})
	}
}

// assertCopies fails unless the target holds exactly want copies of this
// edge and the mapping table holds exactly one row per copy. A leak in
// this engine shows as a copy nothing accounts for, so the count is
// checked on both sides before anything is read out of them.
func assertCopies(t *testing.T, h *harness, want int) []provider.Event {
	t.Helper()

	copies := h.copies()
	if len(copies) != want {
		t.Fatalf("copies in the target: got %d, want %d", len(copies), want)
	}

	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != want {
		t.Fatalf("mapping rows: got %d, want %d", got, want)
	}

	return copies
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
