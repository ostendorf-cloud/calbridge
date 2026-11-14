// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// setup is what one test needs that differs from the ordinary case: an
// edge at a detail level, between a source and a target whose
// capabilities the test chooses.
type setup struct {
	sourceCaps provider.Capabilities
	targetCaps provider.Capabilities
	level      storage.DetailLevel
	managed    bool
	targetZone string
	window     sync.Window
	now        time.Time
}

// defaultSourceCaps is a provider that can do everything, so a test that
// is not about the source's gaps has none.
var defaultSourceCaps = provider.Capabilities{
	PushNotifications: true,
	NativeRecurrence:  true,
	CalendarCreation:  true,
	Attendees:         true,
	FloatingTimes:     true,
}

// defaultTargetCaps is a provider that stores attendees and creates
// calendars, and holds no recurrence rule, which is the case the
// expansion fallback exists for.
var defaultTargetCaps = provider.Capabilities{
	CalendarCreation: true,
	Attendees:        true,
}

// testNow is the moment every pass is measured from, so a rolling window
// covers the same occurrences on every run.
var testNow = time.Date(2026, time.March, 2, 8, 0, 0, 0, time.UTC)

// harness is one edge between two in-memory providers, with the rows
// behind it.
type harness struct {
	t         *testing.T
	store     *fakeStore
	binder    *fakeBinder
	engine    *sync.Engine
	source    *fakeConnector
	target    *fakeConnector
	sourceCal string
	targetCal string
	binding   sync.Binding
	user      uuid.UUID
	clock     *time.Time
}

// setDetailLevel changes what the edge copies, the way a user editing
// the edge does: the stored row and the binding both move.
func (h *harness) setDetailLevel(level storage.DetailLevel) {
	h.t.Helper()

	mask, err := sync.MaskFor(level)
	if err != nil {
		h.t.Fatalf("mask for %q: %v", level, err)
	}

	h.binding.Mask = mask
	h.binding.Edge.DetailLevel = level
	h.store.addEdge(h.binding.Edge)
	h.binder.add(h.binding)
}

// moveNow moves the moment every pass is measured from, which is how a
// rolling window is made to roll inside a test.
func (h *harness) moveNow(at time.Time) {
	h.t.Helper()

	*h.clock = at
}

// newHarness builds an edge from one calendar into another, both empty.
func newHarness(t *testing.T, cfg setup) *harness {
	t.Helper()

	if cfg.sourceCaps == (provider.Capabilities{}) {
		cfg.sourceCaps = defaultSourceCaps
	}
	if cfg.targetCaps == (provider.Capabilities{}) {
		cfg.targetCaps = defaultTargetCaps
	}
	if cfg.level == "" {
		cfg.level = storage.DetailLevelFull
	}
	if cfg.targetZone == "" {
		cfg.targetZone = "Europe/Berlin"
	}
	if cfg.now.IsZero() {
		cfg.now = testNow
	}

	source := newFakeConnector(cfg.sourceCaps)
	target := newFakeConnector(cfg.targetCaps)
	sourceCal := source.AddCalendar(testConnection, provider.Calendar{Name: "Privat", TimeZone: "Europe/Berlin"}).ID
	targetCal := target.AddCalendar(testConnection, provider.Calendar{Name: "Arbeit", TimeZone: cfg.targetZone, Managed: cfg.managed}).ID

	store := newFakeStore()
	user := uuid.New()
	sourceRow := store.addCalendar(user, sourceCal, false)
	targetRow := store.addCalendar(user, targetCal, cfg.managed)
	edge := store.addEdge(storage.Edge{
		UserID:           user,
		SourceCalendarID: sourceRow.ID,
		TargetCalendarID: targetRow.ID,
		DetailLevel:      cfg.level,
	})

	mask, err := sync.MaskFor(cfg.level)
	if err != nil {
		t.Fatalf("mask for %q: %v", cfg.level, err)
	}

	binding := sync.Binding{
		Edge:   edge,
		Mask:   mask,
		Source: sync.Endpoint{Connector: source, Credential: testCredential(), CalendarID: sourceCal, TimeZone: "Europe/Berlin"},
		Target: sync.Endpoint{Connector: target, Credential: testCredential(), CalendarID: targetCal, TimeZone: cfg.targetZone, Managed: cfg.managed},
	}

	binder := newFakeBinder()
	binder.add(binding)

	clock := cfg.now
	engine, err := sync.New(sync.Options{
		Scope:     store,
		Edges:     store.Edges(),
		Calendars: store.Calendars(),
		Grants:    store.Grants(),
		Mappings:  store.Mappings(),
		Binder:    binder,
		Window:    cfg.window,
		Now:       func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	return &harness{
		t:         t,
		store:     store,
		binder:    binder,
		engine:    engine,
		source:    source,
		target:    target,
		sourceCal: sourceCal,
		targetCal: targetCal,
		binding:   binding,
		user:      user,
		clock:     &clock,
	}
}

// put writes an event into the source calendar, the way the user's own
// calendar application does.
func (h *harness) put(event provider.Event) provider.Event {
	h.t.Helper()

	stored := h.source.Put(h.sourceCal, event)
	if stored.ID == "" {
		h.t.Fatal("source calendar did not store the event")
	}

	return stored
}

// replace changes an event already in the source calendar, the way the
// user edits one they already have. It refuses an event with no id, so a
// test meaning to change a fixture cannot add a second one instead.
func (h *harness) replace(event provider.Event) provider.Event {
	h.t.Helper()

	stored, err := h.source.Replace(h.sourceCal, event)
	if err != nil {
		h.t.Fatalf("replace %q in the source calendar: %v", event.ID, err)
	}

	return stored
}

// apply hands one source event to the engine as an upsert change.
func (h *harness) apply(event provider.Event) sync.Report {
	h.t.Helper()

	report, err := h.engine.Apply(context.Background(), h.binding, provider.Change{
		Kind:    provider.ChangeKindUpsert,
		EventID: event.ID,
		Event:   event,
	})
	if err != nil {
		h.t.Fatalf("apply %q: %v", event.ID, err)
	}

	return report
}

// reconcile runs a full pass over the edge.
func (h *harness) reconcile() sync.Report {
	h.t.Helper()

	report, err := h.engine.Reconcile(context.Background(), h.binding)
	if err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}

	return report
}

// copies returns the events the edge wrote into the target.
func (h *harness) copies() []provider.Event {
	h.t.Helper()

	var out []provider.Event
	for _, event := range h.target.Events(h.targetCal) {
		if sync.WrittenBy(event, h.binding.Edge.ID) {
			out = append(out, event)
		}
	}

	return out
}

// onlyCopy returns the single event the edge wrote, failing where there
// is not exactly one.
func (h *harness) onlyCopy() provider.Event {
	h.t.Helper()

	copies := h.copies()
	if len(copies) != 1 {
		h.t.Fatalf("want exactly one copy in the target, got %d", len(copies))
	}

	return copies[0]
}

// meeting is an ordinary event in the source calendar's own zone.
func meeting(summary string, day, hour int) provider.Event {
	return provider.Event{
		Summary:      summary,
		Description:  "Agenda im Anhang",
		Location:     "Raum 2.14",
		Start:        provider.Zoned(time.Date(2026, time.March, day, hour, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:          provider.Zoned(time.Date(2026, time.March, day, hour+1, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Status:       provider.StatusConfirmed,
		Transparency: provider.TransparencyOpaque,
		Visibility:   provider.VisibilityPrivate,
		Attendees:    []provider.Attendee{{Email: "lena@example.com", Name: "Lena Berg", Response: provider.ResponseAccepted}},
	}
}

// actions renders a report as "action/reason" lines, which is what a
// test compares: an action alone does not say which mechanism took it.
func actions(report sync.Report) []string {
	out := make([]string, 0, len(report.Operations))
	for _, op := range report.Operations {
		out = append(out, op.Action.String()+"/"+op.Reason.String())
	}

	return out
}

// wantActions fails unless the report holds exactly these
// action and reason pairs, in order.
func wantActions(t *testing.T, report sync.Report, want ...string) {
	t.Helper()

	got := actions(report)
	if len(got) != len(want) {
		t.Fatalf("operations: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("operation %d: got %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// wantWrites fails unless the target connector was asked for exactly
// this many creates, updates and deletes. An identical rewrite leaves the
// target looking the same, so the only place a write that did not happen
// is visible is the call that was never made.
func wantWrites(t *testing.T, c *fakeConnector, creates, updates, deletes int) {
	t.Helper()

	gotCreates, gotUpdates, gotDeletes := c.Counts()
	if gotCreates != creates || gotUpdates != updates || gotDeletes != deletes {
		t.Fatalf("connector calls: got %d creates, %d updates, %d deletes; want %d, %d, %d",
			gotCreates, gotUpdates, gotDeletes, creates, updates, deletes)
	}
}
