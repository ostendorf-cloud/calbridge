// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// Endpoint is one side of an edge, resolved to what a connector needs to
// reach it. The engine never builds one itself: a credential is the
// connector's own type, and only the layer that loaded the connection can
// produce it.
type Endpoint struct {
	// Connector is the provider this calendar lives in.
	Connector provider.Connector
	// Credential selects the account inside that provider.
	Credential provider.Credential
	// CalendarID is the provider's own id for the calendar.
	CalendarID string
	// TimeZone is the calendar's default IANA zone, which anchors a
	// floating time written into a provider that stores none.
	TimeZone string
	// Managed reports that calbridge created this calendar and owns it, so
	// tearing the edge down removes the calendar as well.
	Managed bool
}

// validate reports whether the endpoint names a calendar the engine can
// reach.
func (ep Endpoint) validate(side string) error {
	switch {
	case ep.Connector == nil:
		return fmt.Errorf("%s endpoint has no connector: %w", side, ErrInvalidBinding)
	case ep.Credential == nil:
		return fmt.Errorf("%s endpoint has no credential: %w", side, ErrInvalidBinding)
	case ep.CalendarID == "":
		return fmt.Errorf("%s endpoint names no calendar: %w", side, ErrInvalidBinding)
	default:
		return nil
	}
}

// Binding is one edge resolved to the two calendars it moves events
// between, and the mask it copies at.
type Binding struct {
	// Edge is the stored edge.
	Edge storage.Edge
	// Mask is what the edge copies: its detail level's preset, with the
	// user's own field overrides already applied.
	Mask FieldMask
	// Source is the calendar read from.
	Source Endpoint
	// Target is the calendar written to.
	Target Endpoint
}

// validate reports whether the binding is one the engine can act on.
func (b Binding) validate() error {
	if b.Edge.ID == uuid.Nil {
		return fmt.Errorf("binding names no edge: %w", ErrInvalidBinding)
	}
	if b.Edge.UserID == uuid.Nil {
		return fmt.Errorf("edge %s names no user: %w", b.Edge.ID, ErrInvalidBinding)
	}
	if err := b.Source.validate("source"); err != nil {
		return fmt.Errorf("edge %s: %w", b.Edge.ID, err)
	}
	if err := b.Target.validate("target"); err != nil {
		return fmt.Errorf("edge %s: %w", b.Edge.ID, err)
	}

	return nil
}

// marker is the tag this edge puts on the copy it makes of one source
// event.
func (b Binding) marker(sourceEventID string) provider.Marker {
	return provider.Marker{Edge: b.Edge.ID.String(), Source: sourceEventID}
}

// nativeRecurrence reports whether a repeating source event is copied as
// one master carrying its rule. Both halves have to agree: the target has
// to store a rule, and the mask has to copy one.
func (b Binding) nativeRecurrence() bool {
	return b.Mask.Has(FieldRecurrence) && b.Target.Connector.Capabilities().NativeRecurrence
}

// Binder resolves a stored edge into the two endpoints it connects. It is
// defined here because the engine is its only caller, and implemented by
// the layer that holds the connections and the connector registry.
type Binder interface {
	Bind(ctx context.Context, edge storage.Edge) (Binding, error)
}

// Options are the engine's dependencies. Every repository is reached
// inside a storage.UserScope.WithUser transaction the engine opens
// itself, so a provider call never sits inside an open database
// transaction.
type Options struct {
	// Scope opens the per-user transaction every repository call needs.
	Scope storage.UserScope
	// Edges reads and removes the edges a pass runs over.
	Edges storage.EdgeRepository
	// Calendars answers which of an edge's calendars its own user owns.
	Calendars storage.CalendarRepository
	// Grants answers which calendars somebody else shared with that user.
	Grants storage.CalendarGrantRepository
	// Mappings is the record of every copy an edge made.
	Mappings storage.EventMappingRepository
	// Binder resolves a stored edge to its two calendars.
	Binder Binder
	// Window is the span a repeating event is expanded into where the
	// target cannot hold the rule. The zero value means DefaultWindow.
	Window Window
	// Now reads the current moment, which the rolling window is measured
	// from. The zero value means time.Now.
	Now func() time.Time
	// Logger receives the degradations a pass survives. The zero value
	// discards them.
	Logger *slog.Logger
}

// Engine applies edges. Every method is safe to call concurrently for
// different edges; two passes over one edge at the same time would write
// the same copies twice and are the caller's to prevent.
type Engine struct {
	scope     storage.UserScope
	edges     storage.EdgeRepository
	calendars storage.CalendarRepository
	grants    storage.CalendarGrantRepository
	mappings  storage.EventMappingRepository
	binder    Binder
	window    Window
	now       func() time.Time
	log       *slog.Logger
}

// New builds an engine, reporting every missing dependency at once rather
// than one per attempt.
func New(opts Options) (*Engine, error) {
	var missing []string

	if opts.Scope == nil {
		missing = append(missing, "scope")
	}
	if opts.Edges == nil {
		missing = append(missing, "edges")
	}
	if opts.Calendars == nil {
		missing = append(missing, "calendars")
	}
	if opts.Grants == nil {
		missing = append(missing, "grants")
	}
	if opts.Mappings == nil {
		missing = append(missing, "mappings")
	}
	if opts.Binder == nil {
		missing = append(missing, "binder")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing %s: %w", strings.Join(missing, ", "), ErrIncompleteOptions)
	}

	window := opts.Window
	if !window.Valid() {
		window = DefaultWindow
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Engine{
		scope:     opts.Scope,
		edges:     opts.Edges,
		calendars: opts.Calendars,
		grants:    opts.Grants,
		mappings:  opts.Mappings,
		binder:    opts.Binder,
		window:    window,
		now:       now,
		log:       logger,
	}, nil
}

// Apply turns one source change into target operations. It is what a
// push notification leads to; a scheduled pass calls Reconcile instead,
// which sees the whole calendar and can repair what a single change
// cannot.
func (e *Engine) Apply(ctx context.Context, b Binding, change provider.Change) (Report, error) {
	if err := b.validate(); err != nil {
		return Report{}, err
	}
	if err := change.Validate(); err != nil {
		return Report{}, fmt.Errorf("edge %s: %w", b.Edge.ID, err)
	}

	if change.Kind == provider.ChangeKindDelete {
		return e.applyDelete(ctx, b, change.EventID)
	}

	current := e.pass()
	if change.Event.Recurrence.IsMaster() {
		// A repeating event is the one case a single change has to know
		// what this edge already wrote: which key space the copies sit in
		// depends on a strategy that may have changed since.
		mappings, err := e.listMappings(ctx, b)
		if err != nil {
			return Report{}, err
		}
		current.mappings = mappings
	}

	return e.upsert(ctx, b, change.Event, current)
}

// pass is what one call read before it started writing: the two
// calendars, the edge's mapping rows, and the rolling window measured
// once for the whole call.
//
// source and target are nil for a single change, which sees one event
// rather than two calendars. The decisions they inform, an instance
// overriding its own occurrence and a copy somebody edited by hand, are
// repaired on the next full pass instead.
type pass struct {
	source   map[string]provider.Event
	target   map[string]provider.Event
	mappings []storage.EventMapping
	from     time.Time
	to       time.Time
}

// pass starts a call, fixing the rolling window for its whole duration so
// two events of one pass are never measured against two different
// moments.
func (e *Engine) pass() *pass {
	from, to := e.window.bounds(e.now())

	return &pass{from: from, to: to}
}

// Reconcile is the full pass. It reads both calendars from the zero sync
// token, which is an initial sync, so it depends on no stored position
// and on no push notification having been delivered. It copies what is
// new, overwrites what drifted in either direction, removes the copies
// whose source is gone, and removes the marked events in the target that
// no mapping row claims.
func (e *Engine) Reconcile(ctx context.Context, b Binding) (Report, error) {
	if err := b.validate(); err != nil {
		return Report{}, err
	}

	current := e.pass()

	var err error
	if current.source, err = snapshot(ctx, b.Source); err != nil {
		return Report{}, fmt.Errorf("edge %s: read source calendar: %w", b.Edge.ID, err)
	}
	if current.target, err = snapshot(ctx, b.Target); err != nil {
		return Report{}, fmt.Errorf("edge %s: read target calendar: %w", b.Edge.ID, err)
	}

	// The mapping rows are read once for the whole pass. Every row written
	// during it is one this pass produced, so a row added after this read
	// is never one the pass has to clean up.
	if current.mappings, err = e.listMappings(ctx, b); err != nil {
		return Report{}, err
	}

	var (
		report Report
		errs   []error
	)

	for _, id := range sortedKeys(current.source) {
		copied, err := e.upsert(ctx, b, current.source[id], current)
		report.merge(copied)
		if err != nil {
			errs = append(errs, err)
		}
	}

	removed, accounted, err := e.removeUnsourced(ctx, b, current.source)
	report.merge(removed)
	if err != nil {
		errs = append(errs, err)
	}

	// Both calendars were read before the pass started writing, so every
	// copy this pass already acted on is accounted for, whether a mapping
	// row still names it or the pass just deleted it.
	for _, op := range report.Operations {
		if op.TargetEventID != "" {
			accounted[op.TargetEventID] = true
		}
	}

	orphans, err := e.removeOrphans(ctx, b, current.target, accounted)
	report.merge(orphans)
	if err != nil {
		errs = append(errs, err)
	}

	return report, errors.Join(errs...)
}

// removeUnsourced deletes the copies this edge no longer writes to, and
// returns every target event a mapping row accounted for, the ones it
// just deleted included: the target was read before those deletions, so
// the orphan sweep would otherwise see them again.
func (e *Engine) removeUnsourced(ctx context.Context, b Binding, sourceState map[string]provider.Event) (Report, map[string]bool, error) {
	mappings, err := e.listMappings(ctx, b)
	if err != nil {
		return Report{}, nil, err
	}

	var (
		report    Report
		errs      []error
		accounted = map[string]bool{}
	)

	for _, mapping := range mappings {
		accounted[mapping.TargetEventID] = true

		if sourced(b, mapping, sourceState) {
			continue
		}

		op, err := e.removeCopy(ctx, b, mapping, unsourcedReason(mapping, sourceState))
		if err != nil {
			errs = append(errs, err)

			continue
		}
		report.add(op)
	}

	return report, accounted, errors.Join(errs...)
}

// removeOrphans deletes the events in the target that carry this edge's
// marker but no mapping row accounts for. That is what an interrupted
// run leaves behind: the copy was written and the mapping row was not.
func (e *Engine) removeOrphans(ctx context.Context, b Binding, targetState map[string]provider.Event, accounted map[string]bool) (Report, error) {
	var (
		report Report
		errs   []error
	)

	for _, id := range sortedKeys(targetState) {
		event := targetState[id]
		if accounted[id] || !WrittenBy(event, b.Edge.ID) {
			continue
		}

		if err := b.Target.Connector.DeleteEvent(ctx, b.Target.Credential, b.Target.CalendarID, id); err != nil {
			errs = append(errs, fmt.Errorf("edge %s: delete orphaned copy %q: %w", b.Edge.ID, id, err))

			continue
		}
		report.add(Operation{SourceEventID: event.Marker.Source, TargetEventID: id, Action: ActionDelete, Reason: ReasonOrphan})
	}

	return report, errors.Join(errs...)
}

// upsert copies one source event, expanding it first where it repeats and
// the target cannot hold the rule.
func (e *Engine) upsert(ctx context.Context, b Binding, src provider.Event, p *pass) (Report, error) {
	if IsOwnWrite(src) {
		return reportOf(Operation{SourceEventID: src.ID, Action: ActionNone, Reason: ReasonOwnWrite}), nil
	}

	if src.Recurrence.IsInstance() {
		_, masterPresent := p.source[src.Recurrence.MasterID]
		if p.source == nil || masterPresent {
			return reportOf(Operation{SourceEventID: src.ID, Action: ActionNone, Reason: ReasonCoveredByMaster}), nil
		}
	}

	if src.Recurrence.IsMaster() && !b.nativeRecurrence() {
		return e.expand(ctx, b, src, p)
	}

	op, err := e.write(ctx, b, src.ID, src, p)
	if err != nil {
		return Report{}, err
	}
	report := reportOf(op)

	if src.Recurrence.IsMaster() {
		// The edge carries the rule itself now. An earlier pass may have
		// expanded this same master into one copy per occurrence, and
		// those copies are keyed where nothing writes any more, so they
		// are removed here rather than left standing at whatever detail
		// level they were written at. Every such row belongs to the
		// strategy that is not in use, so the window bounds decide nothing
		// here and are passed as the pass read them.
		stale, err := e.removeStaleCopies(ctx, b, src.ID, map[string]bool{src.ID: true}, p.mappings, p.from, p.to)
		report.merge(stale)
		if err != nil {
			return report, err
		}
	}

	return report, nil
}

// expand copies a repeating event as one event per occurrence inside the
// rolling window, which is what a target that stores no rule, or a mask
// that drops the rule, leaves as the only way to carry the series.
func (e *Engine) expand(ctx context.Context, b Binding, master provider.Event, p *pass) (Report, error) {
	parsed, err := parseRule(master.Recurrence.Rule)
	if err != nil {
		return Report{}, fmt.Errorf("edge %s: event %q: %w", b.Edge.ID, master.ID, err)
	}

	from, to, err := windowIn(p.from, p.to, master.Start)
	if err != nil {
		return Report{}, fmt.Errorf("edge %s: event %q: %w", b.Edge.ID, master.ID, err)
	}
	starts, err := parsed.occurrences(master.Start, from, to)
	if err != nil {
		return Report{}, fmt.Errorf("edge %s: event %q: %w", b.Edge.ID, master.ID, err)
	}

	duration := master.End.Wall.Sub(master.Start.Wall)
	exceptions := instancesOf(master.ID, p.source)

	var (
		report   Report
		errs     []error
		produced = map[string]bool{}
	)

	for _, start := range starts {
		id := occurrenceID(master.ID, start)
		produced[id] = true

		occurrence, ok := exceptions[stampKey(start.Wall)]
		if !ok {
			occurrence = master
			occurrence.Recurrence = provider.Recurrence{}
			occurrence.Start = start
			occurrence.End = provider.EventTime{Kind: master.End.Kind, Wall: start.Wall.Add(duration), Zone: master.End.Zone}
		}

		op, err := e.write(ctx, b, id, occurrence, p)
		if err != nil {
			errs = append(errs, err)

			continue
		}
		report.add(op)
	}

	dropped, err := e.removeStaleCopies(ctx, b, master.ID, produced, p.mappings, from, to)
	report.merge(dropped)
	if err != nil {
		errs = append(errs, err)
	}

	return report, errors.Join(errs...)
}

// removeStaleCopies deletes the copies of one repeating event that the
// strategy in use no longer writes to: the rows under the other
// strategy's key after the edge switched between copying the rule and
// expanding it, the occurrences the rule stopped producing, and the ones
// that left the rolling window.
//
// A full pass would reach the strategy change through sourced as well,
// which is the one place that decides a row's fate. This runs for two
// things sourced cannot: it names the strategy change and the window
// exit in the report rather than reporting a missing source, and it is
// the only cleanup on the single change path, which has no calendar to
// sweep against.
//
// produced is every key the current strategy wrote this pass: the
// master's own id where the rule is copied, and one id per occurrence
// where it is expanded. from and to are the rolling window as wall clock
// readings in the series' own zone, which only the expansion path has
// and only it reads.
func (e *Engine) removeStaleCopies(ctx context.Context, b Binding, masterID string, produced map[string]bool, mappings []storage.EventMapping, from, to time.Time) (Report, error) {
	expanded := !produced[masterID]

	var (
		report Report
		errs   []error
	)

	for _, mapping := range mappings {
		if produced[mapping.SourceEventID] {
			continue
		}
		if mapping.SourceEventID != masterID && !isOccurrenceOf(mapping.SourceEventID, masterID) {
			continue
		}

		op, err := e.removeCopy(ctx, b, mapping, staleReason(mapping.SourceEventID, masterID, expanded, from, to))
		if err != nil {
			errs = append(errs, err)

			continue
		}
		report.add(op)
	}

	return report, errors.Join(errs...)
}

// staleReason says why a copy of one repeating event is no longer
// written to. A key belonging to the strategy that is not in use is a
// strategy change; an occurrence outside the rolling window left it; the
// rest are occurrences the rule itself stopped producing, which is what
// an added EXDATE or a lowered COUNT looks like.
func staleReason(sourceEventID, masterID string, expanded bool, from, to time.Time) Reason {
	start, isOccurrence := occurrenceStart(sourceEventID, masterID)
	if isOccurrence != expanded {
		return ReasonStrategyChanged
	}
	if !isOccurrence {
		return ReasonSourceGone
	}

	if start.Before(from) || start.After(to) {
		return ReasonOutOfWindow
	}

	return ReasonSourceGone
}

// write is the one place an event reaches the target. It projects the
// source event through the mask, and writes only where the content hash
// says the target does not already hold exactly that.
func (e *Engine) write(ctx context.Context, b Binding, sourceEventID string, src provider.Event, p *pass) (Operation, error) {
	want, err := b.Mask.project(src, b.Target, b.marker(sourceEventID))
	if err != nil {
		return Operation{}, fmt.Errorf("edge %s: project %q: %w", b.Edge.ID, sourceEventID, err)
	}
	hash := contentHash(want, b.Mask)

	mapping, found, err := e.findMapping(ctx, b, sourceEventID)
	if err != nil {
		return Operation{}, err
	}
	if !found {
		return e.create(ctx, b, sourceEventID, want, hash, storage.EventMapping{}, ReasonNone)
	}

	reason := ReasonSourceChanged
	if mapping.ContentHash == hash {
		if held, ok := p.target[mapping.TargetEventID]; ok || p.target == nil {
			if p.target == nil || contentHash(held, b.Mask) == hash {
				return Operation{
					SourceEventID: sourceEventID,
					TargetEventID: mapping.TargetEventID,
					Action:        ActionNone,
					Reason:        ReasonUnchanged,
				}, nil
			}
			reason = ReasonHandEdited
		}
	}

	want.ID = mapping.TargetEventID
	stored, err := b.Target.Connector.UpdateEvent(ctx, b.Target.Credential, b.Target.CalendarID, want)
	if errors.Is(err, provider.ErrUnknownEvent) {
		// The copy is gone from the target, by a deletion calbridge did
		// not make. Writing it again is what a reconcile is for.
		want.ID = ""

		return e.create(ctx, b, sourceEventID, want, hash, mapping, ReasonRestored)
	}
	if err != nil {
		return Operation{}, fmt.Errorf("edge %s: update copy of %q: %w", b.Edge.ID, sourceEventID, err)
	}

	row, err := mappingRow(b, sourceEventID, stored.ID, want, hash)
	if err != nil {
		return Operation{}, err
	}
	row.ID = mapping.ID
	if err := e.scope.WithUser(ctx, b.Edge.UserID, func(ctx context.Context) error {
		_, err := e.mappings.Update(ctx, row)

		return err
	}); err != nil {
		return Operation{}, fmt.Errorf("edge %s: record copy of %q: %w", b.Edge.ID, sourceEventID, err)
	}

	return Operation{
		SourceEventID: sourceEventID,
		TargetEventID: stored.ID,
		Action:        ActionUpdate,
		Reason:        reason,
	}, nil
}

// create writes a new copy into the target and records it. existing is
// the mapping row to point at the new copy, or the zero value where this
// source event has none yet.
func (e *Engine) create(ctx context.Context, b Binding, sourceEventID string, want provider.Event, hash string, existing storage.EventMapping, reason Reason) (Operation, error) {
	stored, err := b.Target.Connector.CreateEvent(ctx, b.Target.Credential, b.Target.CalendarID, want)
	if err != nil {
		return Operation{}, fmt.Errorf("edge %s: create copy of %q: %w", b.Edge.ID, sourceEventID, err)
	}

	row, err := mappingRow(b, sourceEventID, stored.ID, want, hash)
	if err != nil {
		return Operation{}, err
	}

	err = e.scope.WithUser(ctx, b.Edge.UserID, func(ctx context.Context) error {
		if existing.ID != uuid.Nil {
			row.ID = existing.ID
			_, err := e.mappings.Update(ctx, row)

			return err
		}

		_, err := e.mappings.Create(ctx, row)

		return err
	})
	if err != nil {
		return Operation{}, fmt.Errorf("edge %s: record copy of %q: %w", b.Edge.ID, sourceEventID, err)
	}

	return Operation{
		SourceEventID: sourceEventID,
		TargetEventID: stored.ID,
		Action:        ActionCreate,
		Reason:        reason,
	}, nil
}

// applyDelete removes the copies of one source event. A master that was
// expanded holds one copy per occurrence, and all of them go.
func (e *Engine) applyDelete(ctx context.Context, b Binding, sourceEventID string) (Report, error) {
	mappings, err := e.listMappings(ctx, b)
	if err != nil {
		return Report{}, err
	}

	var (
		report Report
		errs   []error
	)

	for _, mapping := range mappings {
		if mapping.SourceEventID != sourceEventID && !isOccurrenceOf(mapping.SourceEventID, sourceEventID) {
			continue
		}

		op, err := e.removeCopy(ctx, b, mapping, ReasonSourceGone)
		if err != nil {
			errs = append(errs, err)

			continue
		}
		report.add(op)
	}

	if len(report.Operations) == 0 && len(errs) == 0 {
		report.add(Operation{SourceEventID: sourceEventID, Action: ActionNone, Reason: ReasonUnmapped})
	}

	return report, errors.Join(errs...)
}

// removeCopy deletes one copy from the target and forgets its mapping
// row. Deleting an event that is already gone succeeds at the connector,
// so an interrupted run is safe to repeat.
func (e *Engine) removeCopy(ctx context.Context, b Binding, mapping storage.EventMapping, reason Reason) (Operation, error) {
	if err := b.Target.Connector.DeleteEvent(ctx, b.Target.Credential, b.Target.CalendarID, mapping.TargetEventID); err != nil {
		return Operation{}, fmt.Errorf("edge %s: delete copy %q: %w", b.Edge.ID, mapping.TargetEventID, err)
	}

	if err := e.scope.WithUser(ctx, b.Edge.UserID, func(ctx context.Context) error {
		return e.mappings.Delete(ctx, mapping.ID)
	}); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return Operation{}, fmt.Errorf("edge %s: forget copy %q: %w", b.Edge.ID, mapping.TargetEventID, err)
	}

	return Operation{
		SourceEventID: mapping.SourceEventID,
		TargetEventID: mapping.TargetEventID,
		Action:        ActionDelete,
		Reason:        reason,
	}, nil
}

// mappingRow builds the mapping table row recording one copy.
func mappingRow(b Binding, sourceEventID, targetEventID string, written provider.Event, hash string) (storage.EventMapping, error) {
	startsAt, err := instant(written.Start)
	if err != nil {
		return storage.EventMapping{}, fmt.Errorf("edge %s: copy of %q: start: %w", b.Edge.ID, sourceEventID, err)
	}
	endsAt, err := instant(written.End)
	if err != nil {
		return storage.EventMapping{}, fmt.Errorf("edge %s: copy of %q: end: %w", b.Edge.ID, sourceEventID, err)
	}
	cached, err := details(written, b.Mask)
	if err != nil {
		return storage.EventMapping{}, fmt.Errorf("edge %s: copy of %q: %w", b.Edge.ID, sourceEventID, err)
	}

	return storage.EventMapping{
		UserID:        b.Edge.UserID,
		EdgeID:        b.Edge.ID,
		SourceEventID: sourceEventID,
		TargetEventID: targetEventID,
		StartsAt:      startsAt,
		EndsAt:        endsAt,
		ContentHash:   hash,
		Details:       cached,
	}, nil
}

// findMapping reads the mapping row for one source event.
func (e *Engine) findMapping(ctx context.Context, b Binding, sourceEventID string) (storage.EventMapping, bool, error) {
	var mapping storage.EventMapping

	err := e.scope.WithUser(ctx, b.Edge.UserID, func(ctx context.Context) error {
		found, err := e.mappings.FindByEdgeAndSourceEventID(ctx, b.Edge.ID, sourceEventID)
		if err != nil {
			return err
		}
		mapping = found

		return nil
	})
	if errors.Is(err, storage.ErrNotFound) {
		return storage.EventMapping{}, false, nil
	}
	if err != nil {
		return storage.EventMapping{}, false, fmt.Errorf("edge %s: read mapping of %q: %w", b.Edge.ID, sourceEventID, err)
	}

	return mapping, true, nil
}

// listMappings reads every copy the edge made.
func (e *Engine) listMappings(ctx context.Context, b Binding) ([]storage.EventMapping, error) {
	var mappings []storage.EventMapping

	if err := e.scope.WithUser(ctx, b.Edge.UserID, func(ctx context.Context) error {
		found, err := e.mappings.ListByEdge(ctx, b.Edge.ID)
		if err != nil {
			return err
		}
		mappings = found

		return nil
	}); err != nil {
		return nil, fmt.Errorf("edge %s: read mappings: %w", b.Edge.ID, err)
	}

	return mappings, nil
}

// sourced reports whether this edge still writes to a mapping row's key.
// It is the one place that question is answered, and it answers it from
// the same facts upsert decides on, so a key nothing writes to any more
// is removed however it got that way.
//
// Which key a copy sits under depends on the strategy, not only on the
// source event existing. A repeating event the edge expands is copied
// under one key per occurrence and not under its own id; an event that
// stops repeating is copied under its own id and no longer under any
// occurrence key. Asking here rather than on the paths that handle a
// repeating event is what covers the routes where the event stops being
// one, which those paths never run for.
func sourced(b Binding, mapping storage.EventMapping, sourceState map[string]provider.Event) bool {
	if src, ok := sourceState[mapping.SourceEventID]; ok {
		return writesUnderOwnID(b, src, sourceState)
	}

	masterID, _, found := strings.Cut(mapping.SourceEventID, occurrenceSeparator)
	if !found {
		return false
	}

	master, ok := sourceState[masterID]
	if !ok {
		return false
	}

	return master.Recurrence.IsMaster() && !b.nativeRecurrence()
}

// writesUnderOwnID reports whether this edge copies a source event under
// the event's own id. It mirrors upsert's own decisions: a repeating
// event the edge expands is copied under one key per occurrence instead,
// and an occurrence the edge covers through its master is not copied on
// its own at all.
func writesUnderOwnID(b Binding, src provider.Event, sourceState map[string]provider.Event) bool {
	if src.Recurrence.IsMaster() {
		return b.nativeRecurrence()
	}
	if src.Recurrence.IsInstance() {
		_, masterPresent := sourceState[src.Recurrence.MasterID]

		return !masterPresent
	}

	return true
}

// unsourcedReason says why nothing writes to a key any more: the source
// event is gone, or it is still in the calendar and this edge stopped
// writing under that key, which is what a repeating event losing its
// rule, or an edge changing strategy, looks like.
func unsourcedReason(mapping storage.EventMapping, sourceState map[string]provider.Event) Reason {
	if _, ok := sourceState[mapping.SourceEventID]; ok {
		return ReasonStrategyChanged
	}

	masterID, _, found := strings.Cut(mapping.SourceEventID, occurrenceSeparator)
	if !found {
		return ReasonSourceGone
	}
	if _, ok := sourceState[masterID]; ok {
		return ReasonStrategyChanged
	}

	return ReasonSourceGone
}

// instancesOf indexes the source calendar's own instances of one master
// by the start the master's rule gives them, so an occurrence somebody
// moved or retitled is copied as the source holds it.
func instancesOf(masterID string, sourceState map[string]provider.Event) map[string]provider.Event {
	out := map[string]provider.Event{}
	for _, event := range sourceState {
		if event.Recurrence.MasterID == masterID {
			out[stampKey(event.Recurrence.OriginalStart.Wall)] = event
		}
	}

	return out
}

// maxPages bounds one calendar read, so a connector handing out pages
// forever fails instead of running forever.
const maxPages = 1000

// snapshot reads a calendar in full, from the zero sync token. That is an
// initial sync: every event the calendar holds now, and no deletion, so a
// full pass needs neither a stored position nor a delivered notification.
func snapshot(ctx context.Context, ep Endpoint) (map[string]provider.Event, error) {
	events := map[string]provider.Event{}

	var token provider.SyncToken
	for range maxPages {
		page, err := ep.Connector.Changes(ctx, ep.Credential, ep.CalendarID, token)
		if err != nil {
			return nil, fmt.Errorf("read changes: %w", err)
		}
		if err := page.Validate(); err != nil {
			return nil, fmt.Errorf("read changes: %w", err)
		}

		for _, change := range page.Changes {
			if change.Kind == provider.ChangeKindDelete {
				delete(events, change.EventID)

				continue
			}
			events[change.EventID] = change.Event
		}

		if !page.State.More() {
			return events, nil
		}
		token = page.Token
	}

	return nil, fmt.Errorf("calendar %q: %w", ep.CalendarID, ErrTooManyPages)
}

// sortedKeys returns a map's keys in a fixed order, so a pass applies its
// events the same way twice.
func sortedKeys(events map[string]provider.Event) []string {
	keys := make([]string, 0, len(events))
	for key := range events {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	return keys
}
