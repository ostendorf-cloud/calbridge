// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
//
// It names its master and the start the master's rule gave it, which is
// what caldav.decodeEvent sets on every override it reads, and what
// provider.Recurrence.Validate demands of one.
func overrideOf(master provider.Event, day int) provider.Event {
	event := meeting(master.Summary, day, 10)
	event.ID = provider.InstanceIDOf(master.ID, occurrenceID)
	event.Marker = master.Marker
	event.Recurrence = provider.Recurrence{
		MasterID:      master.ID,
		OriginalStart: provider.Zoned(time.Date(2026, time.March, day, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}

	return event
}

// occurrenceID is what a provider identifies one overridden occurrence
// by, an RFC 5545 RECURRENCE-ID for CalDAV, and it stays opaque outside
// the connector. occurrenceLayout reads one back, so a fixture naming an
// occurrence and the start it names stay one value.
const (
	occurrenceID     = "20260304T090000"
	occurrenceLayout = "20060102T150405"
)

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

// strayMasterID is the object a strayOverride sits in, and
// otherStrayMasterID a second one. No event in the calendar answers to
// either id on its own, which is the whole shape: the object is there
// because an override is in it. The separator sorts before the dash, so
// the overrides in strayMasterID's object are the ones a pass meets
// first.
const (
	strayMasterID      = "evt-never-written"
	otherStrayMasterID = "evt-never-written-either"
)

// strayOverride is an object in the target holding one override of this
// edge's own making and no master: an override naming a master that is
// not in the calendar, carrying this edge's marker. It is the shape a
// user leaves behind by deleting the master's own copy out of the object
// the override sits in.
func strayOverride(t *testing.T, h *harness, occurrence string) provider.Event {
	t.Helper()

	return strayOverrideIn(t, h, strayMasterID, occurrence)
}

// strayOverrideIn places one in the object a given master would be in,
// for a test that needs two such objects rather than two overrides of
// one.
func strayOverrideIn(t *testing.T, h *harness, masterID, occurrence string) provider.Event {
	t.Helper()

	stray := meeting("Standup", 4, 9)
	stray.ID = provider.InstanceIDOf(masterID, occurrence)
	stray.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}
	original, err := time.Parse(occurrenceLayout, occurrence)
	if err != nil {
		t.Fatalf("parse the occurrence %q: %v", occurrence, err)
	}
	stray.Recurrence = provider.Recurrence{
		MasterID:      masterID,
		OriginalStart: provider.Zoned(original, "Europe/Berlin"),
	}

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
	stray := strayOverride(t, h, occurrenceID)

	report, err := h.engine.Teardown(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	wantActions(t, report, "delete/master absent")
	wantDeletes(t, target, strayMasterID)

	if got := report.Operations[0].TargetEventID; got != strayMasterID {
		t.Errorf("the operation names %q, want the object the delete asked for", got)
	}
	// The operation carries the source of the override's own marker, which
	// is the only source id this object can be traced to: no mapping row
	// names it, and the master it belongs to is gone.
	if got := report.Operations[0].SourceEventID; got != stray.Marker.Source {
		t.Errorf("the operation names source %q, want the one the override's marker carries, %q", got, stray.Marker.Source)
	}
	if got := len(h.target.Events(h.targetCal)); got != 0 {
		t.Errorf("the target still holds %d events after the object was removed", got)
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
	stray := strayOverride(t, h, occurrenceID)

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "delete/master absent")
	wantDeletes(t, target, strayMasterID)

	if got := report.Operations[0].TargetEventID; got != strayMasterID {
		t.Errorf("the operation names %q, want the object the delete asked for", got)
	}
	if h.target.Holds(h.targetCal, stray.ID) {
		t.Error("the override survived the removal of the object it sits in")
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
	strayOverride(t, h, occurrenceID)
	strayOverride(t, h, "20260311T090000")

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "delete/master absent")
	wantDeletes(t, target, strayMasterID)

	if got := len(h.target.Events(h.targetCal)); got != 0 {
		t.Errorf("the target still holds %d events, want none: one object held both overrides", got)
	}
}

func TestEngine_Reconcile_removesTheObjectAnOverrideNamesAndNotThePrefixOfItsID(t *testing.T) {
	t.Parallel()

	// A master's own id can carry the separator: the CalDAV resource
	// "a%23b.ics" reaches the connector as the event id "a#b.ics", and an
	// override inside that resource is reported under that id plus its
	// occurrence. Cutting the override's id at the first separator yields
	// "a", an object nobody named, so the id has to come from the event:
	// Recurrence.MasterID is what the connector read off the resource.
	//
	// The id is built here rather than through provider.InstanceIDOf, so
	// the fixture rests on no helper that answers on an id's shape.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()

	const masterID = "a" + provider.InstanceSeparator + "b.ics"

	override := meeting("Standup", 4, 9)
	override.ID = masterID + provider.InstanceSeparator + occurrenceID
	override.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}
	override.Recurrence = provider.Recurrence{
		MasterID:      masterID,
		OriginalStart: provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
	if stored := h.target.Put(h.targetCal, override); stored.ID != override.ID {
		t.Fatalf("the target stored the override as %q, want %q", stored.ID, override.ID)
	}

	_, err := h.engine.Reconcile(context.Background(), h.binding)

	// The id the delete asked for is the whole point, and the calendar
	// cannot be asked: the object and the occurrence go together.
	wantDeletes(t, target, masterID)

	// What the connector then does with that id is issue #29's: a master id
	// carrying the separator is refused by every connector declaring no
	// instance writes, the fake and caldav alike, so the object stays and
	// the refusal reaches the caller.
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Errorf("Reconcile() error = %v, want ErrUnsupported from the refused id", err)
	}
	if !h.target.Holds(h.targetCal, override.ID) {
		t.Error("the override is gone after a delete the connector refused")
	}
}

func TestEngine_Reconcile_sweepsNoObjectForAnEventThatOnlyLooksLikeAnOverride(t *testing.T) {
	t.Parallel()

	// A plain event id is the server's to choose, and a CalDAV resource
	// named "a%23b.ics" reaches the connector as the event id "a#b.ics".
	// Read as an occurrence it names an object "a" this edge may never have
	// written, so nothing is derived from the id: the event itself says
	// whether it is an override, and this one does not.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()

	collision := meeting("Standup", 4, 9)
	collision.ID = "a" + provider.InstanceSeparator + "b.ics"
	collision.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}
	if stored := h.target.Put(h.targetCal, collision); stored.ID != collision.ID {
		t.Fatalf("the target stored the event as %q, want %q", stored.ID, collision.ID)
	}

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Its own id is one the target refuses, and it names no master, so no
	// id in the calendar removes it. It stays, and the report says why.
	wantActions(t, report, "none/no deletable id")
	wantDeletes(t, target)

	if !h.target.Holds(h.targetCal, collision.ID) {
		t.Error("the pass removed the event whose id only reads as an occurrence")
	}
	if got := report.Operations[0].TargetEventID; got != collision.ID {
		t.Errorf("the operation names %q, want the event it is about, %q", got, collision.ID)
	}
}

func TestEngine_Reconcile_removesTheObjectOfAnOverrideWhoseMasterWasWrittenElsewhere(t *testing.T) {
	t.Parallel()

	// A copy gone from the target is written again, and the target names
	// the new copy itself, so the master's object is not the one the
	// mapping row named a moment ago. The object the row named still holds
	// the override, it is still this edge's own write, and no master covers
	// it any more.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	master := h.onlyCopy()
	override := putOverride(t, h, master)

	// The client rewrote the object with only the override left in it,
	// which is the one deletion no id of the target's reaches.
	h.target.Forget(h.targetCal, master.ID)
	target := h.recordTargetDeletes()

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "create/restored", "delete/master absent")
	wantDeletes(t, target, master.ID)

	if h.target.Holds(h.targetCal, override.ID) {
		t.Error("the override survived, reported as covered by a master the pass wrote somewhere else")
	}
	if got := len(h.copies()); got != 1 {
		t.Errorf("copies in the target: got %d, want the one the pass wrote again", got)
	}
}

func TestEngine_Reconcile_reportsNoCoverFromAMasterTheSamePassRemoved(t *testing.T) {
	t.Parallel()

	// The copy whose source is gone is deleted in an earlier stage, and
	// the target was read before that. The override inside the object is
	// still in that read, and the master still with it, so the report
	// would say an occurrence is covered by a master the same pass took
	// out of the calendar.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	source := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	master := h.onlyCopy()
	override := putOverride(t, h, master)

	if err := h.source.DeleteEvent(context.Background(), testCredential(), h.sourceCal, source.ID); err != nil {
		t.Fatalf("delete the source event: %v", err)
	}
	target := h.recordTargetDeletes()

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// One delete, one line for it, and nothing said about the occurrence
	// that went with the object.
	wantActions(t, report, "delete/source gone")
	wantDeletes(t, target, master.ID)

	if h.target.Holds(h.targetCal, override.ID) {
		t.Error("the override survived the deletion of the object its master was in")
	}
}

func TestEngine_Reconcile_keepsTheObjectAMasterWasWrittenIntoDuringThePass(t *testing.T) {
	t.Parallel()

	// The target names a new copy itself, and nothing stops it naming it
	// after an object that was in the calendar before: a CalDAV server
	// picking a resource name is free to reuse one. The target was read
	// before that write, so a master absent from the read can be in the
	// calendar by the time the sweep runs, and deleting its object would
	// destroy the copy this pass wrote a moment earlier.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()

	// The fake names its copies in order, so this is the id the create
	// below is given.
	const reusedID = "evt-1"

	override := strayOverrideIn(t, h, reusedID, occurrenceID)
	h.put(meeting("Jour fixe", 4, 9))

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := report.Operations[0].TargetEventID; got != reusedID {
		t.Fatalf("the copy was written as %q, want the object the override is in, %q", got, reusedID)
	}
	wantActions(t, report, "create/none", "none/covered by master")
	wantDeletes(t, target)

	if !h.target.Holds(h.targetCal, reusedID) {
		t.Error("the pass deleted the object it had just written the copy into")
	}
	if !h.target.Holds(h.targetCal, override.ID) {
		t.Error("the override is gone, and only a delete of its object could have taken it")
	}
}

func TestEngine_Reconcile_spendsOneRequestOnARefusedCredentialSweepingAnObject(t *testing.T) {
	t.Parallel()

	// The object holding an override is deleted from a stage of its own, so
	// the refusal has to be recorded there as well. Two such objects would
	// otherwise cost two refused requests and two errors for one revoked
	// password.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()
	stray := strayOverride(t, h, occurrenceID)
	other := strayOverrideIn(t, h, otherStrayMasterID, occurrenceID)
	h.target.RejectWritesAfter(0)

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Reconcile() error = %v, want ErrCredentialRejected", err)
	}
	if got := countErrors(err); got != 1 {
		t.Errorf("errors collected: got %d, want 1, the refusal that stopped the pass", got)
	}
	wantDeletes(t, target, strayMasterID)

	if len(report.Operations) != 0 {
		t.Errorf("operations reported: %v, want none, the delete was refused", actions(report))
	}
	if !h.target.Holds(h.targetCal, stray.ID) || !h.target.Holds(h.targetCal, other.ID) {
		t.Error("an override is gone after a refused delete")
	}
}

func TestEngine_Reconcile_leavesTheObjectAloneAfterAnEarlierStageWasRefused(t *testing.T) {
	t.Parallel()

	// The stray object is the last thing a pass meets, so a credential
	// already known refused must not buy one more request here either.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	source := h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	copied := h.onlyCopy()
	strayOverride(t, h, occurrenceID)
	if err := h.source.DeleteEvent(context.Background(), testCredential(), h.sourceCal, source.ID); err != nil {
		t.Fatalf("delete the source event: %v", err)
	}

	target := h.recordTargetDeletes()
	h.target.RejectWritesAfter(0)

	if _, err := h.engine.Reconcile(context.Background(), h.binding); !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Reconcile() error = %v, want ErrCredentialRejected", err)
	}

	// The copy whose source is gone is the one refused request. The object
	// holding the override is never asked about.
	wantDeletes(t, target, copied.ID)
}

func TestEngine_Reconcile_asksAboutAnObjectOnceWhenTheDeleteFails(t *testing.T) {
	t.Parallel()

	// A failure that is not a refused credential leaves the pass running,
	// and the object is the same object for every override in it. Asking
	// again would spend a second request on it and report a second error
	// for one failure.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	target := h.recordTargetDeletes()
	stray := strayOverride(t, h, occurrenceID)
	strayOverride(t, h, "20260311T090000")

	failure := errors.New("the server answered 500")
	target.failDelete(strayMasterID, failure)

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if !errors.Is(err, failure) {
		t.Fatalf("Reconcile() error = %v, want the failure the target returned", err)
	}
	if errors.Is(err, provider.ErrCredentialRejected) {
		t.Error("a plain failure was read as a refused credential")
	}
	if got := countErrors(err); got != 1 {
		t.Errorf("errors collected: got %d, want 1, one failure for one object", got)
	}
	wantDeletes(t, target, strayMasterID)

	if len(report.Operations) != 0 {
		t.Errorf("operations reported: %v, want none, the delete failed", actions(report))
	}
	if !h.target.Holds(h.targetCal, stray.ID) {
		t.Error("the override is gone after a failed delete")
	}
}

func TestEngine_Teardown_keepsTheMappingRowsWhenSweepingAnObjectFails(t *testing.T) {
	t.Parallel()

	// The teardown forgets the mapping rows and the edge once the target
	// gave everything back. A sweep that could not remove an object has not
	// given it back, so the failure has to reach the caller and the rows
	// have to outlive it, or the next teardown has nothing left to try.
	h := newHarness(t, setup{targetCaps: nativeTargetCaps, window: shortWindow})
	h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	target := h.recordTargetDeletes()
	stray := strayOverride(t, h, occurrenceID)

	failure := errors.New("the server answered 500")
	target.failDelete(strayMasterID, failure)

	if _, err := h.engine.Teardown(context.Background(), h.binding); !errors.Is(err, failure) {
		t.Fatalf("Teardown() error = %v, want the failure the target returned", err)
	}
	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != 1 {
		t.Errorf("mapping rows after a failed teardown: got %d, want the 1 it started with", got)
	}
	if !h.target.Holds(h.targetCal, stray.ID) {
		t.Error("the override is gone after a failed delete")
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
