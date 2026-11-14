// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

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

// strayMasterID is the object a strayOverride sits in. Nothing in the
// calendar answers to that id on its own, so the fake takes a delete of
// it as one already done and keeps the override it holds. The id the
// engine asked for is what says whether the object was removed.
const strayMasterID = "evt-never-written"

// strayOverride is an object in the target holding one override of this
// edge's own making and no master: an id derived from a master that is
// not in the calendar, carrying this edge's marker. It is the shape a
// user leaves behind by deleting the master's own copy out of the object
// the override sits in.
func strayOverride(t *testing.T, h *harness, occurrence string) provider.Event {
	t.Helper()

	stray := meeting("Standup", 4, 9)
	stray.ID = provider.InstanceIDOf(strayMasterID, occurrence)
	stray.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}

	stored := h.target.Put(h.targetCal, stray)
	if stored.ID != stray.ID {
		t.Fatalf("the target stored the override as %q, want %q", stored.ID, stray.ID)
	}

	return stored
}

func TestEngine_Teardown_removesTheObjectOfAnOverrideWhoseMasterIsGone(t *testing.T) {
	t.Parallel()

	// The teardown sweeps the target for the marker after it has removed
	// the mapped copies, and an object holding overrides and no master is
	// what can be left there. Everything in such an object is this edge's
	// own write, so the teardown gives it back by the one id the target
	// accepts, the master's, and the edge leaves nothing behind.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()
	stray := strayOverride(t, h, "20260304T090000")

	report, err := h.engine.Teardown(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	wantActions(t, report, "delete/master gone")
	wantDeletes(t, target, strayMasterID)

	if got := report.Operations[0].TargetEventID; got != strayMasterID {
		t.Errorf("the operation names %q, want the object the delete asked for", got)
	}
	if got := report.Operations[0].SourceEventID; got != stray.Marker.Source {
		t.Errorf("the operation names source %q, want the one the override's marker carries, %q", got, stray.Marker.Source)
	}
}

func TestEngine_Reconcile_removesTheObjectOfAnOverrideWhoseMasterIsGone(t *testing.T) {
	t.Parallel()

	// A reconcile meets the same object, and has to answer the same way:
	// an occurrence left standing here was removed by no pass and no
	// teardown, and was reported again on every pass for the life of the
	// edge. The master's id is what removes the object, and the target was
	// read in full before the sweep, so a master missing from that read is
	// missing from the calendar.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()
	strayOverride(t, h, "20260304T090000")

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "delete/master gone")
	wantDeletes(t, target, strayMasterID)

	if got := report.Operations[0].TargetEventID; got != strayMasterID {
		t.Errorf("the operation names %q, want the object the delete asked for", got)
	}
}

func TestEngine_Reconcile_removesOneObjectOnceForEveryOverrideItHolds(t *testing.T) {
	t.Parallel()

	// One object holds every override of one master, and the read that
	// found them was taken before the deletion. Asking per occurrence
	// would spend one request per override on an object already gone and
	// report the same deletion twice.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()
	strayOverride(t, h, "20260304T090000")
	strayOverride(t, h, "20260311T090000")

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "delete/master gone")
	wantDeletes(t, target, strayMasterID)
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
