// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// nativeTargetCaps is the shape the shipped iCloud profile has: it stores
// an RFC 5545 rule, so a repeating event is copied as one master, and it
// writes no overridden occurrence on its own, because one CalDAV resource
// holds the master and every override of it.
var nativeTargetCaps = provider.Capabilities{
	CalendarCreation: true,
	Attendees:        true,
	NativeRecurrence: true,
}

// overrideOf is the event a user leaves behind by changing one occurrence
// of a mirrored master in their own calendar application. The provider
// keeps it inside the master's object and reports it under an id derived
// from the master's, and it carries the marker the master's copy carries,
// because the user edited calbridge's own event.
func overrideOf(master provider.Event, day int) provider.Event {
	event := meeting(master.Summary, day, 10)
	event.ID = provider.InstanceIDOf(master.ID, "20260304T090000")
	event.Marker = master.Marker

	return event
}

// putOverride places one in the target and asserts the fixture, because
// the fake gives an event with no id one of its own and an override that
// silently became an ordinary event would prove nothing.
func putOverride(t *testing.T, h *harness, master provider.Event) provider.Event {
	t.Helper()

	want := overrideOf(master, 4)
	stored := h.target.Put(h.targetCal, want)
	if stored.ID != want.ID {
		t.Fatalf("the target stored the override as %q, want %q", stored.ID, want.ID)
	}
	if got := len(h.target.Events(h.targetCal)); got != 2 {
		t.Fatalf("target calendar holds %d events, want the master's copy and the override", got)
	}

	return stored
}

func TestEngine_Reconcile_neverAsksATargetToDeleteAnOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	// The interface promises that a caller reading capabilities first
	// never meets ErrUnsupported. The orphan sweep deletes by whatever id
	// the target's own listing reported, and on the shipped iCloud profile
	// that listing holds an id the target refuses: a user overriding one
	// occurrence of a mirrored master leaves one, carrying this edge's
	// marker and claimed by no mapping row.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	master := h.onlyCopy()
	wantWrites(t, h.target, 1, 0, 0)

	override := putOverride(t, h, master)

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "none/unchanged", "none/covered by master")
	wantWrites(t, h.target, 1, 0, 0)

	if !h.target.Holds(h.targetCal, override.ID) {
		t.Error("the pass removed the override after reporting it as covered")
	}
	if !h.target.Holds(h.targetCal, master.ID) {
		t.Error("the pass removed the master's copy")
	}
}

func TestEngine_Reconcile_clearsAnOverrideOnTheMastersNextWriteAndNotBefore(t *testing.T) {
	t.Parallel()

	// ReasonCoveredByMaster rests on this: the master's object is replaced
	// whole when it is written, so the override inside it goes. What it
	// does not promise is that the write happens soon. A master whose
	// source never changes again is never written again, and the override
	// stands for as long as that lasts.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	source := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	master := h.onlyCopy()
	override := putOverride(t, h, master)

	// Two passes over an unchanged source. Nothing is written, so nothing
	// clears the override: this is the indefinite half of the behaviour.
	for pass := range 2 {
		wantActions(t, h.reconcile(), "none/unchanged", "none/covered by master")
		wantWrites(t, h.target, 1, 0, 0)
		if !h.target.Holds(h.targetCal, override.ID) {
			t.Fatalf("pass %d removed the override with no write to the master", pass+1)
		}
	}

	// The source master changes, so the master is written, and the write
	// replaces the object the override was in.
	source.Summary = "Jour fixe, neuer Titel"
	h.replace(source)

	// The target was read before the write, so this pass still reports the
	// override it found there. The write it makes is what removes it.
	wantActions(t, h.reconcile(), "update/source changed", "none/covered by master")
	wantWrites(t, h.target, 1, 1, 0)

	if h.target.Holds(h.targetCal, override.ID) {
		t.Error("the override survived a write of the master's own object")
	}
	if got := h.onlyCopy().Summary; got != source.Summary {
		t.Errorf("the copy's title is %q, want the source's %q", got, source.Summary)
	}
}

func TestEngine_Teardown_givesBackAnOverriddenOccurrenceThroughItsMaster(t *testing.T) {
	t.Parallel()

	// The edge promises to give back everything it wrote. Removing the
	// master's object removes the override inside it, so the promise is
	// kept without ever naming an id the target refuses.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	master := h.onlyCopy()
	override := putOverride(t, h, master)

	report, err := h.engine.Teardown(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	wantActions(t, report, "delete/edge removed")

	if h.target.Holds(h.targetCal, override.ID) {
		t.Error("the override survived the teardown of the edge that wrote its master")
	}
	if got := len(h.target.Events(h.targetCal)); got != 0 {
		t.Errorf("the target still holds %d events", got)
	}
}

func TestEngine_Teardown_reportsAnOverriddenOccurrenceNothingCanRemove(t *testing.T) {
	t.Parallel()

	// The teardown sweeps the target for the marker after it has removed
	// the mapped copies, and meets the same refusal there: an override
	// whose master is not in the calendar is an id the target will not
	// take. The teardown reports it rather than failing on an operation
	// the capability already ruled out.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})

	stray := meeting("Standup", 4, 9)
	stray.ID = provider.InstanceIDOf("evt-never-written", "20260304T090000")
	stray.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}

	if stored := h.target.Put(h.targetCal, stray); stored.ID != stray.ID {
		t.Fatalf("the target stored the override as %q, want %q", stored.ID, stray.ID)
	}

	report, err := h.engine.Teardown(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	wantActions(t, report, "none/instance unreachable")

	if !h.target.Holds(h.targetCal, stray.ID) {
		t.Error("the teardown removed an event it reported as unreachable")
	}
}

func TestEngine_Reconcile_reportsAnOverriddenOccurrenceNothingCanRemove(t *testing.T) {
	t.Parallel()

	// A resource holding overrides and no master at all leaves an event
	// carrying this edge's marker that no id the target accepts removes.
	// Skipping it silently would leave a copy calbridge claims to own
	// standing in somebody's calendar with nothing to say so.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})

	stray := meeting("Standup", 4, 9)
	stray.ID = provider.InstanceIDOf("evt-never-written", "20260304T090000")
	stray.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}

	if stored := h.target.Put(h.targetCal, stray); stored.ID != stray.ID {
		t.Fatalf("the target stored the override as %q, want %q", stored.ID, stray.ID)
	}

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "none/instance unreachable")
	wantWrites(t, h.target, 0, 0, 0)

	if !h.target.Holds(h.targetCal, stray.ID) {
		t.Error("the pass removed an event it reported as unreachable")
	}
}

func TestEngine_Reconcile_spendsOneRequestOnARefusedCredentialAcrossStages(t *testing.T) {
	t.Parallel()

	// One revoked password must cost one request and one error, whichever
	// stage of the pass meets it. Each stage stopping on its own is not
	// enough: the pass carries on into the next one and spends another
	// request on a credential already known refused.
	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	orphan := meeting("Vergessene Kopie", 6, 11)
	orphan.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}
	h.target.Put(h.targetCal, orphan)

	if err := h.source.DeleteEvent(context.Background(), testCredential(), h.sourceCal, source.ID); err != nil {
		t.Fatalf("delete the source event: %v", err)
	}

	creates, updates, deletes := h.target.Counts()
	h.target.RejectWritesAfter(0)

	_, err := h.engine.Reconcile(context.Background(), h.binding)
	if !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Reconcile() error = %v, want ErrCredentialRejected", err)
	}
	if got := countErrors(err); got != 1 {
		t.Errorf("errors collected: got %d, want 1, the refusal that stopped the pass", got)
	}

	gotCreates, gotUpdates, gotDeletes := h.target.Counts()
	if gotCreates != creates || gotUpdates != updates || gotDeletes != deletes {
		t.Errorf("writes that reached the target after the refusal: %d creates, %d updates, %d deletes",
			gotCreates-creates, gotUpdates-updates, gotDeletes-deletes)
	}
}

func TestEngine_Apply_spendsOneRequestOnARefusedCredential(t *testing.T) {
	t.Parallel()

	// A master this edge expanded holds one copy per occurrence, and a
	// delete walks every one of them. Without a guard that is one refused
	// request per occurrence.
	h := newHarness(t, setup{window: shortWindow})
	master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	if got := len(h.copies()); got != 3 {
		t.Fatalf("copies of the expanded series: got %d, want 3", got)
	}
	h.target.RejectWritesAfter(0)

	_, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindDelete,
		EventID: master.ID,
	})
	if !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Apply() error = %v, want ErrCredentialRejected", err)
	}
	if got := countErrors(err); got != 1 {
		t.Errorf("errors collected: got %d, want 1, the refusal that stopped the call", got)
	}
}

func TestEngine_Reconcile_spendsOneRequestOnARefusedCredentialWhileExpanding(t *testing.T) {
	t.Parallel()

	// The expansion writes one event per occurrence, so the same guard has
	// to hold inside one source event's own loop.
	h := newHarness(t, setup{window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.target.RejectWritesAfter(0)

	_, err := h.engine.Reconcile(context.Background(), h.binding)
	if !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Reconcile() error = %v, want ErrCredentialRejected", err)
	}
	if got := countErrors(err); got != 1 {
		t.Errorf("errors collected: got %d, want 1, the refusal that stopped the pass", got)
	}
	if got := len(h.copies()); got != 0 {
		t.Errorf("copies written on a refused credential: got %d, want none", got)
	}
}

func TestEngine_Reconcile_leavesTheOccurrencesItNeverReachedAlone(t *testing.T) {
	t.Parallel()

	// A refused credential stops the expansion partway. The copies the
	// pass never reached belong to the strategy still in use, so the sweep
	// that follows must not read them as copies nothing writes to.
	h := newHarness(t, setup{window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	before := len(h.copies())
	if before != 3 {
		t.Fatalf("copies of the expanded series: got %d, want 3", before)
	}

	h.target.Edit(h.targetCal, h.copies()[0].ID, func(event *provider.Event) {
		event.Summary = "Nicht stoeren"
	})
	h.target.RejectWritesAfter(0)

	if _, err := h.engine.Reconcile(context.Background(), h.binding); !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Reconcile() error = %v, want ErrCredentialRejected", err)
	}
	if got := len(h.copies()); got != before {
		t.Errorf("copies after a refused pass: got %d, want the %d it started with", got, before)
	}
	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != before {
		t.Errorf("mapping rows after a refused pass: got %d, want %d", got, before)
	}
}

func TestEngine_Reconcile_normalisesOnlyWhereItComparesAgainstTheTarget(t *testing.T) {
	t.Parallel()

	// Normalising is a full provider mapping, which for CalDAV is an
	// iCalendar encode and decode with a VTIMEZONE build. A first copy has
	// nothing to compare against, so it pays none.
	h := newHarness(t, setup{targetCaps: normalisingTargetCaps})

	var normalised int
	h.target.SetNormalisation(func(event provider.Event) provider.Event {
		normalised++

		return toLineFeeds(event)
	})

	h.put(crlfMeeting("Jour fixe"))
	wantActions(t, h.reconcile(), "create/none")

	// One for the write the fake itself applies, and none for a
	// comparison, because there was no copy to compare against yet.
	if normalised != 1 {
		t.Errorf("normalisations on the first pass: got %d, want 1, the write itself", normalised)
	}
}

// instanceSweepIsDeclarationDriven keeps the two sweeps honest about
// which capability they read.
func TestEngine_Reconcile_deletesAnOverriddenOccurrenceWhereTheTargetTakesOne(t *testing.T) {
	t.Parallel()

	// A provider whose instances are events of their own declares it, and
	// then an orphaned one is an orphan like any other.
	caps := nativeTargetCaps
	caps.InstanceWrites = true

	h := newHarness(t, setup{targetCaps: caps, window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	master := h.onlyCopy()
	override := putOverride(t, h, master)

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "none/unchanged", "delete/orphan")

	if h.target.Holds(h.targetCal, override.ID) {
		t.Error("a target that writes instances kept an orphaned one")
	}
}
