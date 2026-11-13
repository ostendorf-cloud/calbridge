// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

func TestNew(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	complete := sync.Options{
		Scope:     store,
		Edges:     store.Edges(),
		Calendars: store.Calendars(),
		Grants:    store.Grants(),
		Mappings:  store.Mappings(),
		Binder:    newFakeBinder(),
	}

	tests := []struct {
		name    string
		options func(sync.Options) sync.Options
		wantErr error
	}{
		{
			name:    "every dependency given",
			options: func(o sync.Options) sync.Options { return o },
		},
		{
			name:    "no scope",
			options: func(o sync.Options) sync.Options { o.Scope = nil; return o },
			wantErr: sync.ErrIncompleteOptions,
		},
		{
			name:    "no mapping repository",
			options: func(o sync.Options) sync.Options { o.Mappings = nil; return o },
			wantErr: sync.ErrIncompleteOptions,
		},
		{
			name:    "no binder",
			options: func(o sync.Options) sync.Options { o.Binder = nil; return o },
			wantErr: sync.ErrIncompleteOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := sync.New(tt.options(complete))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEngine_Apply_copiesANewSourceEvent(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))

	wantActions(t, h.apply(source), "create/none")
	wantWrites(t, h.target, 1, 0, 0)

	copied := h.onlyCopy()
	if copied.Summary != "Jour fixe" {
		t.Fatalf("summary: got %q, want %q", copied.Summary, "Jour fixe")
	}
	if !copied.Start.Equal(source.Start) || !copied.End.Equal(source.End) {
		t.Fatalf("times: got %s to %s, want %s to %s", copied.Start, copied.End, source.Start, source.End)
	}
	if copied.Marker.Edge != h.binding.Edge.ID.String() || copied.Marker.Source != source.ID {
		t.Fatalf("marker: got %+v, want edge %s and source %q", copied.Marker, h.binding.Edge.ID, source.ID)
	}

	mappings := h.store.mappingsOf(h.binding.Edge.ID)
	if len(mappings) != 1 || mappings[0].SourceEventID != source.ID || mappings[0].TargetEventID != copied.ID {
		t.Fatalf("mapping rows: got %+v", mappings)
	}
}

func TestEngine_Apply_neverCopiesAnEventCalbridgeWroteItself(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})

	// The event arrives as an ordinary source change, and carries the
	// marker another edge wrote on it. The marker, not its content, is
	// what stops it.
	marked := meeting("Mirrored already", 4, 9)
	marked.Marker = provider.Marker{Edge: uuid.NewString(), Source: "evt-upstream"}
	stored := h.put(marked)

	wantActions(t, h.apply(stored), "none/own write")
	wantWrites(t, h.target, 0, 0, 0)

	if copies := h.copies(); len(copies) != 0 {
		t.Fatalf("target holds %d copies, want none", len(copies))
	}
	if mappings := h.store.mappingsOf(h.binding.Edge.ID); len(mappings) != 0 {
		t.Fatalf("mapping rows: got %d, want none", len(mappings))
	}
}

func TestEngine_Apply_writesNothingWhenTheContentIsUnchanged(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))

	wantActions(t, h.apply(source), "create/none")
	wantActions(t, h.apply(source), "none/unchanged")

	// The target would look identical either way, so the content hash
	// only shows as a call the connector never received.
	wantWrites(t, h.target, 1, 0, 0)
}

func TestEngine_Apply_updatesWhenTheSourceChanged(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))
	h.apply(source)

	source.Summary = "Jour fixe, verlegt"
	source = h.put(source)

	wantActions(t, h.apply(source), "update/source changed")
	wantWrites(t, h.target, 1, 1, 0)

	if got := h.onlyCopy().Summary; got != "Jour fixe, verlegt" {
		t.Fatalf("summary: got %q, want %q", got, "Jour fixe, verlegt")
	}
}

func TestEngine_Apply_removesTheCopyWhenTheSourceEventIsDeleted(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))
	h.apply(source)

	report, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindDelete,
		EventID: source.ID,
	})
	if err != nil {
		t.Fatalf("apply delete: %v", err)
	}

	wantActions(t, report, "delete/source gone")
	wantWrites(t, h.target, 1, 0, 1)

	if copies := h.copies(); len(copies) != 0 {
		t.Fatalf("target holds %d copies, want none", len(copies))
	}
	if mappings := h.store.mappingsOf(h.binding.Edge.ID); len(mappings) != 0 {
		t.Fatalf("mapping rows: got %d, want none", len(mappings))
	}
}

func TestEngine_Apply_reportsADeletionItNeverCopied(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})

	report, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindDelete,
		EventID: "evt-never-seen",
	})
	if err != nil {
		t.Fatalf("apply delete: %v", err)
	}

	wantActions(t, report, "none/unmapped")
	wantWrites(t, h.target, 0, 0, 0)
}

func TestEngine_Apply_copiesWhatTheDetailLevelAllows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		level           storage.DetailLevel
		wantSummary     string
		wantDescription string
		wantLocation    string
		wantAttendees   int
		wantVisibility  provider.Visibility
	}{
		{
			name:            "full copy",
			level:           storage.DetailLevelFull,
			wantSummary:     "Jour fixe",
			wantDescription: "Agenda im Anhang",
			wantLocation:    "Raum 2.14",
			wantAttendees:   1,
			wantVisibility:  provider.VisibilityPrivate,
		},
		{
			name:        "title and time",
			level:       storage.DetailLevelTitleTime,
			wantSummary: "Jour fixe",
		},
		{
			name:  "busy block",
			level: storage.DetailLevelBusyBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, setup{level: tt.level})
			source := h.put(meeting("Jour fixe", 4, 9))
			h.apply(source)

			copied := h.onlyCopy()
			if copied.Summary != tt.wantSummary {
				t.Errorf("summary: got %q, want %q", copied.Summary, tt.wantSummary)
			}
			if copied.Description != tt.wantDescription {
				t.Errorf("description: got %q, want %q", copied.Description, tt.wantDescription)
			}
			if copied.Location != tt.wantLocation {
				t.Errorf("location: got %q, want %q", copied.Location, tt.wantLocation)
			}
			if len(copied.Attendees) != tt.wantAttendees {
				t.Errorf("attendees: got %d, want %d", len(copied.Attendees), tt.wantAttendees)
			}
			if copied.Visibility != tt.wantVisibility {
				t.Errorf("visibility: got %s, want %s", copied.Visibility, tt.wantVisibility)
			}
			// Every preset carries the time and whether it is taken: that
			// is what a busy block is.
			if !copied.Start.Equal(source.Start) || copied.Transparency != source.Transparency {
				t.Errorf("time and transparency: got %s %s, want %s %s",
					copied.Start, copied.Transparency, source.Start, source.Transparency)
			}
		})
	}
}

func TestEngine_Apply_overridesOnePresetField(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{level: storage.DetailLevelBusyBlock})
	// The user keeps the busy block preset and asks for the location as
	// well, which is the per-field override the design calls for.
	h.binding.Mask = h.binding.Mask.With(sync.FieldLocation)

	source := h.put(meeting("Jour fixe", 4, 9))
	h.apply(source)

	copied := h.onlyCopy()
	if copied.Location != "Raum 2.14" {
		t.Fatalf("location: got %q, want %q", copied.Location, "Raum 2.14")
	}
	if copied.Summary != "" {
		t.Fatalf("summary: got %q, want it dropped", copied.Summary)
	}
}

func TestEngine_Apply_refusesAFloatingTimeWithNoZoneToAnchorIt(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{targetZone: " "})
	h.binding.Target.TimeZone = ""

	floating := meeting("Lesezeit", 4, 9)
	floating.Start = provider.Floating(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC))
	floating.End = provider.Floating(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC))
	source := h.put(floating)

	_, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindUpsert,
		EventID: source.ID,
		Event:   source,
	})
	if !errors.Is(err, sync.ErrNoTargetZone) {
		t.Fatalf("got %v, want %v", err, sync.ErrNoTargetZone)
	}
	wantWrites(t, h.target, 0, 0, 0)
}

func TestEngine_Apply_anchorsAFloatingTimeInTheTargetCalendarsZone(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{targetZone: "America/New_York"})

	floating := meeting("Lesezeit", 4, 9)
	floating.Start = provider.Floating(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC))
	floating.End = provider.Floating(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC))
	source := h.put(floating)
	h.apply(source)

	copied := h.onlyCopy()
	if copied.Start.Kind != provider.TimeKindZoned || copied.Start.Zone != "America/New_York" {
		t.Fatalf("start: got %s, want the same wall time in America/New_York", copied.Start)
	}
}

func TestEngine_Apply_dropsWhatTheTargetCannotStoreAndStaysIdempotent(t *testing.T) {
	t.Parallel()

	// A target that does not store attendees drops them on write. The
	// projection has to drop them too, or the hash of what the engine
	// wrote would never match the hash of what the provider holds, and
	// every pass would write again.
	h := newHarness(t, setup{targetCaps: provider.Capabilities{CalendarCreation: true}})
	source := h.put(meeting("Jour fixe", 4, 9))

	h.apply(source)
	if got := len(h.onlyCopy().Attendees); got != 0 {
		t.Fatalf("attendees in the target: got %d, want none", got)
	}

	wantActions(t, h.reconcile(), "none/unchanged")
	wantWrites(t, h.target, 1, 0, 0)
}

func TestEngine_Reconcile_copiesWithoutAnyChangeNotification(t *testing.T) {
	t.Parallel()

	// Nothing was ever applied: the events were put into the source
	// calendar and no notification was delivered. A full pass reads the
	// calendar from the zero sync token and copies them anyway.
	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))
	h.put(meeting("Retro", 5, 14))

	wantActions(t, h.reconcile(), "create/none", "create/none")
	if got := len(h.copies()); got != 2 {
		t.Fatalf("copies: got %d, want 2", got)
	}
}

func TestEngine_Reconcile_writesNothingOnASecondPass(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))
	h.put(meeting("Retro", 5, 14))

	h.reconcile()
	wantActions(t, h.reconcile(), "none/unchanged", "none/unchanged")
	wantWrites(t, h.target, 2, 0, 0)
}

func TestEngine_Reconcile_overwritesACopyEditedByHandInTheTarget(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	copied := h.onlyCopy()
	h.target.edit(h.targetCal, copied.ID, func(event *provider.Event) {
		event.Summary = "Nicht stoeren"
		event.Location = "Zuhause"
	})

	wantActions(t, h.reconcile(), "update/hand edited")

	restored := h.onlyCopy()
	if restored.Summary != source.Summary || restored.Location != source.Location {
		t.Fatalf("copy: got %q at %q, want the source's %q at %q",
			restored.Summary, restored.Location, source.Summary, source.Location)
	}
}

func TestEngine_Reconcile_writesACopySomebodyDeletedAgain(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	copied := h.onlyCopy()
	h.target.edit(h.targetCal, copied.ID, func(event *provider.Event) {})
	if err := h.target.DeleteEvent(context.Background(), fakeCredential{connection: testConnection}, h.targetCal, copied.ID); err != nil {
		t.Fatalf("delete the copy behind calbridge's back: %v", err)
	}

	wantActions(t, h.reconcile(), "create/restored")
	if got := len(h.copies()); got != 1 {
		t.Fatalf("copies: got %d, want 1", got)
	}
}

func TestEngine_Reconcile_removesACopyWhoseSourceIsGone(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	source := h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	if err := h.source.DeleteEvent(context.Background(), fakeCredential{connection: testConnection}, h.sourceCal, source.ID); err != nil {
		t.Fatalf("delete the source event: %v", err)
	}

	wantActions(t, h.reconcile(), "delete/source gone")
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies: got %d, want none", got)
	}
	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != 0 {
		t.Fatalf("mapping rows: got %d, want none", got)
	}
}

func TestEngine_Reconcile_removesAMarkedEventNoMappingClaims(t *testing.T) {
	t.Parallel()

	// A run cut short between the write and the mapping row leaves a copy
	// nothing owns. The marker is what lets the next pass recognise it.
	h := newHarness(t, setup{})
	orphan := meeting("Vergessene Kopie", 6, 11)
	orphan.Marker = provider.Marker{Edge: h.binding.Edge.ID.String(), Source: "evt-gone"}
	h.target.put(h.targetCal, orphan)

	wantActions(t, h.reconcile(), "delete/orphan")
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies: got %d, want none", got)
	}
}

func TestEngine_Reconcile_leavesEventsOfAnotherEdgeAlone(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	foreign := meeting("Fremder Termin", 6, 11)
	stored := h.target.put(h.targetCal, foreign)

	wantActions(t, h.reconcile())

	if !h.target.holds(h.targetCal, stored.ID) {
		t.Fatal("the target lost an event this edge never wrote")
	}
}

func TestEngine_neverCopiesACopyOnward(t *testing.T) {
	t.Parallel()

	// The first edge fills a calendar with copies. A second edge reads
	// that calendar as its own source: every event in it carries a
	// marker, so nothing is copied onward and the mirror ends there.
	first := newHarness(t, setup{})
	first.put(meeting("Jour fixe", 4, 9))
	first.reconcile()

	onward := newFakeConnector(defaultTargetCaps)
	onwardCal := onward.addCalendar("Weiter", provider.Calendar{TimeZone: "Europe/Berlin"})
	onwardRow := first.store.addCalendar(first.user, onwardCal, false)
	targetRow := first.store.addCalendar(first.user, first.targetCal, false)
	second := sync.Binding{
		Edge: first.store.addEdge(storage.Edge{
			UserID:           first.user,
			SourceCalendarID: targetRow.ID,
			TargetCalendarID: onwardRow.ID,
			DetailLevel:      storage.DetailLevelFull,
		}),
		Mask:   mustMask(t, storage.DetailLevelFull),
		Source: sync.Endpoint{Connector: first.target, Credential: fakeCredential{connection: testConnection}, CalendarID: first.targetCal, TimeZone: "Europe/Berlin"},
		Target: sync.Endpoint{Connector: onward, Credential: fakeCredential{connection: testConnection}, CalendarID: onwardCal, TimeZone: "Europe/Berlin"},
	}

	report, err := first.engine.Reconcile(context.Background(), second)
	if err != nil {
		t.Fatalf("reconcile the second edge: %v", err)
	}

	wantActions(t, report, "none/own write")
	wantWrites(t, onward, 0, 0, 0)
}

func TestEngine_Teardown_givesBackEverythingTheEdgeWrote(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))
	h.put(meeting("Retro", 5, 14))
	h.reconcile()

	foreign := h.target.put(h.targetCal, meeting("Fremder Termin", 6, 11))

	report, err := h.engine.Teardown(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}

	wantActions(t, report, "delete/edge removed", "delete/edge removed")
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies left in the target: got %d, want none", got)
	}
	if got := len(h.store.mappingsOf(h.binding.Edge.ID)); got != 0 {
		t.Fatalf("mapping rows: got %d, want none", got)
	}
	if !h.target.holds(h.targetCal, foreign.ID) {
		t.Fatal("teardown removed an event this edge never wrote")
	}
	if report.CalendarDeleted {
		t.Fatal("teardown removed a calendar the user picked")
	}
}

func TestEngine_Teardown_removesACalendarCalbridgeCreated(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{managed: true})
	h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	report, err := h.engine.Teardown(context.Background(), h.binding)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}

	if !report.CalendarDeleted {
		t.Fatal("teardown kept the calendar calbridge created")
	}
	if events := h.target.events(h.targetCal); events != nil {
		t.Fatalf("the managed calendar still holds %d events", len(events))
	}
}

func TestEngine_DeleteEdge_removesTheEdgeItself(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	if _, err := h.engine.DeleteEdge(context.Background(), h.binding); err != nil {
		t.Fatalf("delete edge: %v", err)
	}

	if h.store.hasEdge(h.binding.Edge.ID) {
		t.Fatal("the edge row is still there")
	}
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies left in the target: got %d, want none", got)
	}
}

func TestEngine_Run_reconcilesEveryEdgeOfTheUser(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	h.put(meeting("Jour fixe", 4, 9))

	report, err := h.engine.Run(context.Background(), h.user)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	wantActions(t, report, "create/none")
}

func TestEngine_Run_tearsDownAnEdgeNoGrantCoversAnyMore(t *testing.T) {
	t.Parallel()

	// The edge's own user owns the target but not the source: a grant is
	// what let them build it. The database checked that when the edge was
	// written and does not check it again, so revoking the grant leaves an
	// edge reading a calendar nobody shares any more.
	h := newHarness(t, setup{})
	h.store.dropEdge(h.binding.Edge.ID)

	owner := uuid.New()
	sourceRow := h.store.addCalendar(owner, h.sourceCal, false)

	edge := h.store.addEdge(storage.Edge{
		UserID:           h.user,
		SourceCalendarID: sourceRow.ID,
		TargetCalendarID: h.binding.Edge.TargetCalendarID,
		DetailLevel:      storage.DetailLevelFull,
	})
	binding := h.binding
	binding.Edge = edge
	h.binder.add(binding)
	h.binding = binding

	grant := h.store.addGrant(storage.CalendarGrant{
		OwnerUserID:    owner,
		GranteeUserID:  h.user,
		CalendarID:     sourceRow.ID,
		MaxDetailLevel: storage.DetailLevelFull,
	})

	h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()
	if got := len(h.copies()); got != 1 {
		t.Fatalf("copies before the revocation: got %d, want 1", got)
	}

	h.store.dropGrant(grant.ID)

	report, err := h.engine.Run(context.Background(), h.user)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	wantActions(t, report, "delete/grant revoked")
	if got := len(h.copies()); got != 0 {
		t.Fatalf("copies after the revocation: got %d, want none", got)
	}
	if h.store.hasEdge(edge.ID) {
		t.Fatal("the edge still exists after its grant was revoked")
	}
}

func TestEngine_EnforceGrants_stopsTheEdgesOverOneCalendar(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	owner := uuid.New()
	sourceRow := h.store.addCalendar(owner, h.sourceCal, false)

	edge := h.store.addEdge(storage.Edge{
		UserID:           h.user,
		SourceCalendarID: sourceRow.ID,
		TargetCalendarID: h.binding.Edge.TargetCalendarID,
		DetailLevel:      storage.DetailLevelTitleTime,
	})
	binding := h.binding
	binding.Edge = edge
	binding.Mask = mustMask(t, storage.DetailLevelTitleTime)
	h.binder.add(binding)
	h.binding = binding

	// The grant narrows to a busy block, which the edge's title and time
	// level copies more than.
	h.store.addGrant(storage.CalendarGrant{
		OwnerUserID:    owner,
		GranteeUserID:  h.user,
		CalendarID:     sourceRow.ID,
		MaxDetailLevel: storage.DetailLevelBusyBlock,
	})

	h.put(meeting("Jour fixe", 4, 9))
	h.reconcile()

	report, err := h.engine.EnforceGrants(context.Background(), h.user, sourceRow.ID)
	if err != nil {
		t.Fatalf("enforce grants: %v", err)
	}

	wantActions(t, report, "delete/grant revoked")
	if h.store.hasEdge(edge.ID) {
		t.Fatal("the edge still exists after exceeding its grant")
	}
}

func TestEngine_CheckEdge_refusesAnOpposingEdge(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})

	opposing := storage.Edge{
		UserID:           h.user,
		SourceCalendarID: h.binding.Edge.TargetCalendarID,
		TargetCalendarID: h.binding.Edge.SourceCalendarID,
		DetailLevel:      storage.DetailLevelFull,
	}

	err := h.engine.CheckEdge(context.Background(), opposing)
	if !errors.Is(err, sync.ErrLoop) {
		t.Fatalf("got %v, want %v", err, sync.ErrLoop)
	}
}

func TestEngine_CheckEdge_admitsAnEdgeIntoAThirdCalendar(t *testing.T) {
	t.Parallel()

	h := newHarness(t, setup{})
	third := h.store.addCalendar(h.user, "cal-third", false)

	candidate := storage.Edge{
		UserID:           h.user,
		SourceCalendarID: h.binding.Edge.SourceCalendarID,
		TargetCalendarID: third.ID,
		DetailLevel:      storage.DetailLevelBusyBlock,
	}

	if err := h.engine.CheckEdge(context.Background(), candidate); err != nil {
		t.Fatalf("check edge: %v", err)
	}
}

// mustMask returns a detail level's preset mask or fails the test.
func mustMask(t *testing.T, level storage.DetailLevel) sync.FieldMask {
	t.Helper()

	mask, err := sync.MaskFor(level)
	if err != nil {
		t.Fatalf("mask for %q: %v", level, err)
	}

	return mask
}
