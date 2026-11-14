// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/providertest"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// normalisingTargetCaps is a target that rewrites content on write and
// says so, which is what the CalDAV connector is: an iCalendar object
// carries one escape for a newline, so a description written with a
// carriage return reads back with a line feed.
var normalisingTargetCaps = provider.Capabilities{
	CalendarCreation:  true,
	Attendees:         true,
	NormalisesContent: true,
}

// toLineFeeds is the rewriting such a target applies.
func toLineFeeds(event provider.Event) provider.Event {
	event.Summary = strings.ReplaceAll(event.Summary, "\r\n", "\n")
	event.Description = strings.ReplaceAll(event.Description, "\r\n", "\n")
	event.Location = strings.ReplaceAll(event.Location, "\r\n", "\n")

	return event
}

// crlfMeeting is a source event whose description carries the line
// breaks the target rewrites.
func crlfMeeting(summary string) provider.Event {
	event := meeting(summary, 4, 9)
	event.Description = "Agenda:\r\n1. Rueckblick\r\n2. Planung"

	return event
}

func TestEngine_Reconcile_doesNotRewriteACopyTheTargetNormalised(t *testing.T) {
	t.Parallel()

	// The failure this prevents is silent. Without the declaration the
	// copy the target hands back differs from the projection, the engine
	// reads that as somebody's hand edit, and it rewrites the event on
	// every pass forever while the sync looks like it is working.
	h := newHarness(t, setup{targetCaps: normalisingTargetCaps})
	h.target.SetNormalisation(toLineFeeds)
	h.put(crlfMeeting("Jour fixe"))

	wantActions(t, h.reconcile(), "create/none")
	wantWrites(t, h.target, 1, 0, 0)

	wantActions(t, h.reconcile(), "none/unchanged")
	wantWrites(t, h.target, 1, 0, 0)

	// A third pass is where an every-pass rewrite would be unmistakable.
	wantActions(t, h.reconcile(), "none/unchanged")
	wantWrites(t, h.target, 1, 0, 0)

	if got := h.onlyCopy().Description; got != "Agenda:\n1. Rueckblick\n2. Planung" {
		t.Errorf("the target holds the description as %q", got)
	}
}

func TestEngine_Reconcile_stillOverwritesAHandEditOnANormalisingTarget(t *testing.T) {
	t.Parallel()

	// Reading a round trip difference as content rather than as an edit
	// must not cost the engine the edits it does have to overwrite.
	h := newHarness(t, setup{targetCaps: normalisingTargetCaps})
	h.target.SetNormalisation(toLineFeeds)
	source := h.put(crlfMeeting("Jour fixe"))
	h.reconcile()

	h.target.Edit(h.targetCal, h.onlyCopy().ID, func(event *provider.Event) {
		event.Summary = "Nicht stoeren"
	})

	wantActions(t, h.reconcile(), "update/hand edited")
	wantWrites(t, h.target, 1, 1, 0)

	if got := h.onlyCopy().Summary; got != source.Summary {
		t.Errorf("the copy's title is %q, want the source's %q", got, source.Summary)
	}
}

func TestEngine_Reconcile_refusesATargetThatDeclaresNormalisationAndAnswersNothing(t *testing.T) {
	t.Parallel()

	// Declaring the capability without implementing
	// provider.ContentNormaliser is a programming error in a connector.
	// Catching it when the binding is checked is what keeps it from
	// arriving as the silent rewrite loop instead.
	h := newHarness(t, setup{})
	binding := h.binding
	binding.Target.Connector = silentNormaliser{Connector: h.target}

	_, err := h.engine.Reconcile(context.Background(), binding)
	if !errors.Is(err, sync.ErrInvalidBinding) {
		t.Fatalf("Reconcile() error = %v, want ErrInvalidBinding", err)
	}
	if !errors.Is(err, provider.ErrInvalidCapabilities) {
		t.Errorf("Reconcile() error = %v, want it to name the capability that disagrees", err)
	}
}

// silentNormaliser declares that it rewrites content on write and
// implements no provider.ContentNormaliser. Embedding the interface
// rather than the fake is what leaves the method out.
type silentNormaliser struct {
	provider.Connector
}

func (s silentNormaliser) Capabilities() provider.Capabilities {
	caps := s.Connector.Capabilities()
	caps.NormalisesContent = true

	return caps
}

func TestEngine_Reconcile_stopsAtACredentialTheProviderRefused(t *testing.T) {
	t.Parallel()

	// Every later call on that endpoint fails the same way, so carrying on
	// turns one revoked password into one error and one wasted provider
	// request per event.
	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))
	h.put(meeting("Retro", 5, 14))
	h.put(meeting("Planung", 6, 11))
	h.target.RejectWritesAfter(0)

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if !errors.Is(err, provider.ErrCredentialRejected) {
		t.Fatalf("Reconcile() error = %v, want ErrCredentialRejected", err)
	}
	if got := countErrors(err); got != 1 {
		t.Errorf("errors collected: got %d, want 1, the refusal that stopped the pass", got)
	}
	if got := len(report.Operations); got != 0 {
		t.Errorf("operations: got %d, want none (all: %v)", got, actions(report))
	}

	creates, updates, deletes := h.target.Counts()
	if creates != 0 || updates != 0 || deletes != 0 {
		t.Errorf("writes that reached the target: %d creates, %d updates, %d deletes", creates, updates, deletes)
	}
}

// countErrors reports how many errors an errors.Join holds, which is how
// many events a pass tried before it gave up.
func countErrors(err error) int {
	joined, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // the join itself is what is being counted, not a wrapped cause
	if !ok {
		return 1
	}

	return len(joined.Unwrap())
}

func TestEngine_Apply_deleteOfAMasterRemovesTheOccurrencesUnderIt(t *testing.T) {
	t.Parallel()

	// One source object holding several normalised events is deleted as
	// one object, and the provider has one id to report that under. The
	// overridden occurrences are gone with it, and the caller is what
	// applies the implication, because the object is already gone and
	// cannot be asked what was in it.
	h := newHarness(t, setup{})
	master := h.put(weekly("RRULE:FREQ=WEEKLY;COUNT=3"))
	h.reconcile()

	if got := len(h.copies()); got != 3 {
		t.Fatalf("copies of the expanded series: got %d, want 3", got)
	}

	report, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindDelete,
		EventID: master.ID,
	})
	if err != nil {
		t.Fatalf("apply the master's deletion: %v", err)
	}

	wantActions(t, report, "delete/source gone", "delete/source gone", "delete/source gone")
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies left after the master went: got %d, want none", got)
	}
	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != 0 {
		t.Fatalf("mapping rows left: got %d, want none", got)
	}
}

func TestEngine_Apply_deleteOfAMasterRemovesACopiedOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	// A source provider that gives an overridden occurrence an id derived
	// from its master's reports one deletion, naming the master. The copy
	// of the occurrence sits under an id of that shape, and nothing else
	// would ever name it again.
	h := newHarness(t, setup{})

	masterID := "evt-master"
	override := meeting("Standup", 4, 9)
	override.ID = provider.InstanceIDOf(masterID, "20260304T090000")
	override.Recurrence = provider.Recurrence{
		MasterID:      masterID,
		OriginalStart: provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}

	// The master itself is not in the source snapshot, so the occurrence
	// is copied under its own id rather than through its master. Assert
	// the fixture before the probe: the fake gives an event with no id one
	// of its own, and an override that silently became an ordinary event
	// would prove nothing here.
	stored := h.source.Put(h.sourceCal, override)
	if stored.ID != override.ID {
		t.Fatalf("the source stored the override as %q, want %q", stored.ID, override.ID)
	}
	if got := len(h.source.Events(h.sourceCal)); got != 1 {
		t.Fatalf("source calendar holds %d events, want exactly the override", got)
	}

	h.reconcile()
	if got := len(h.copies()); got != 1 {
		t.Fatalf("copies of the override: got %d, want 1", got)
	}

	report, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindDelete,
		EventID: masterID,
	})
	if err != nil {
		t.Fatalf("apply the master's deletion: %v", err)
	}

	wantActions(t, report, "delete/source gone")
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies left after the master went: got %d, want none", got)
	}
}

func TestEngine_neverAsksATargetToWriteAnOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	// A target declaring no instance writes refuses one with
	// provider.ErrUnsupported, and the interface promises a caller that
	// read the capability first never meets that error. The engine keeps
	// the promise by copying an occurrence as a plain event at its own
	// time: the source's series means nothing in the target calendar.
	h := newHarness(t, setup{})
	if h.target.Capabilities().InstanceWrites {
		t.Fatal("this test is about a target that declares no instance writes")
	}

	override := meeting("Standup", 4, 9)
	override.ID = provider.InstanceIDOf("evt-master", "20260304T090000")
	override.Recurrence = provider.Recurrence{
		MasterID:      "evt-master",
		OriginalStart: provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
	if stored := h.source.Put(h.sourceCal, override); stored.ID != override.ID {
		t.Fatalf("the source stored the override as %q, want %q", stored.ID, override.ID)
	}

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	wantActions(t, report, "create/none")

	copied := h.onlyCopy()
	if copied.Recurrence.IsInstance() {
		t.Errorf("the copy names master %q, which the target cannot hold", copied.Recurrence.MasterID)
	}
	if !copied.Start.Equal(override.Start) {
		t.Errorf("the copy starts at %s, want the occurrence's own %s", copied.Start, override.Start)
	}
}

func TestEngine_Reconcile_refusesAnEndpointWhoseConnectorDeclaresNormalisationOnTheSourceSide(t *testing.T) {
	t.Parallel()

	// The source is read, never written, but a connector whose
	// declaration and implementation disagree is wrong either way, and the
	// engine says so before it reads anything.
	h := newHarness(t, setup{})
	binding := h.binding
	binding.Source.Connector = silentNormaliser{Connector: h.source}

	_, err := h.engine.Reconcile(context.Background(), binding)
	if !errors.Is(err, sync.ErrInvalidBinding) {
		t.Fatalf("Reconcile() error = %v, want ErrInvalidBinding", err)
	}
}

func TestEngine_aConnectorThatDeclaresNothingNeedsNoNormaliser(t *testing.T) {
	t.Parallel()

	// Most connectors store an event as written. They implement nothing,
	// and the engine has one path rather than two.
	plain := providertest.New(provider.Capabilities{CalendarCreation: true})

	normaliser, err := provider.NormaliserOf(plain)
	if err != nil {
		t.Fatalf("normaliser of a connector that declares none: %v", err)
	}

	event := meeting("Jour fixe", 4, 9)
	event.Description = "Agenda:\r\n1. Rueckblick"

	got, err := normaliser.NormaliseEvent(event)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if got.Description != event.Description {
		t.Errorf("the event came back as %q, want it unchanged", got.Description)
	}
}
