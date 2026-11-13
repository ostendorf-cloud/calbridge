// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// Run is the scheduled full pass over one user's edges. It reconciles
// every edge a grant still covers, and tears down the ones no grant
// covers any more. One edge failing does not stop the others: the pass
// returns what it did together with the errors it collected.
//
// The schedule itself belongs to the caller. Nothing here depends on a
// push notification having been delivered, which is what makes a pass
// the floor under every edge rather than a repair for an unlucky one.
func (e *Engine) Run(ctx context.Context, userID uuid.UUID) (Report, error) {
	var edges []storage.Edge

	if err := e.scope.WithUser(ctx, userID, func(ctx context.Context) error {
		found, err := e.edges.List(ctx)
		if err != nil {
			return err
		}
		edges = found

		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("list edges of user %s: %w", userID, err)
	}

	var (
		report Report
		errs   []error
	)

	for _, edge := range edges {
		ran, err := e.runEdge(ctx, edge)
		report.merge(ran)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return report, errors.Join(errs...)
}

// runEdge reconciles one edge, or tears it down where its grant is gone.
func (e *Engine) runEdge(ctx context.Context, edge storage.Edge) (Report, error) {
	coverage, err := e.Coverage(ctx, edge)
	if err != nil {
		return Report{}, err
	}

	if err := CheckGrants(edge, coverage); err != nil {
		if !errors.Is(err, ErrGrantMissing) && !errors.Is(err, ErrGrantExceeded) {
			return Report{}, err
		}

		e.log.Warn("tearing down an edge no grant covers", "edge", edge.ID, "user", edge.UserID, "reason", err.Error())

		return e.remove(ctx, edge, ReasonGrantRevoked)
	}

	binding, err := e.binder.Bind(ctx, edge)
	if err != nil {
		return Report{}, fmt.Errorf("bind edge %s: %w", edge.ID, err)
	}

	return e.Reconcile(ctx, binding)
}

// EnforceGrants checks the edges a grant covered and tears down the ones
// no grant covers any more. It is what a revoked grant leads to: the
// database refuses an edge over a calendar nobody shared when the edge is
// written, and revoking the grant afterwards does not touch the edge, so
// the invariant only holds where something keeps checking it.
//
// It takes the grant row rather than a user and a calendar because the
// edges belong to the grantee and are invisible to anybody else. A
// caller passing the wrong user would read an empty edge list and a nil
// error, which is indistinguishable from nothing to do, while the edges
// kept writing. The row carries the only pair that can be right.
func (e *Engine) EnforceGrants(ctx context.Context, grant storage.CalendarGrant) (Report, error) {
	var edges []storage.Edge

	if err := e.scope.WithUser(ctx, grant.GranteeUserID, func(ctx context.Context) error {
		found, err := e.edges.ListByCalendar(ctx, grant.CalendarID)
		if err != nil {
			return err
		}
		edges = found

		return nil
	}); err != nil {
		return Report{}, fmt.Errorf("list edges over calendar %s: %w", grant.CalendarID, err)
	}

	var (
		report Report
		errs   []error
	)

	for _, edge := range edges {
		coverage, err := e.Coverage(ctx, edge)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		err = CheckGrants(edge, coverage)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrGrantMissing) && !errors.Is(err, ErrGrantExceeded) {
			errs = append(errs, err)

			continue
		}

		e.log.Warn("tearing down an edge no grant covers", "edge", edge.ID, "user", edge.UserID, "reason", err.Error())

		removed, err := e.remove(ctx, edge, ReasonGrantRevoked)
		report.merge(removed)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return report, errors.Join(errs...)
}

// DeleteEdge gives back everything the edge wrote and removes the edge
// itself. It is the one way an edge disappears: the copies go first, so
// an interrupted deletion leaves an edge with fewer copies rather than
// copies nothing owns.
func (e *Engine) DeleteEdge(ctx context.Context, b Binding) (Report, error) {
	report, err := e.Teardown(ctx, b)
	if err != nil {
		return report, err
	}

	if err := e.deleteEdgeRow(ctx, b.Edge); err != nil {
		return report, err
	}

	return report, nil
}

// Teardown deletes every event the edge wrote, forgets every mapping row
// it held, and removes the target calendar where calbridge created it. A
// calendar the user picked is left standing, with the events that were
// not this edge's untouched.
func (e *Engine) Teardown(ctx context.Context, b Binding) (Report, error) {
	if err := b.validate(); err != nil {
		return Report{}, err
	}

	return e.tearDown(ctx, b, ReasonEdgeRemoved)
}

// remove binds an edge, tears down what it wrote and deletes the edge
// row, which is what both a revoked grant and a deleted edge come down
// to.
func (e *Engine) remove(ctx context.Context, edge storage.Edge, reason Reason) (Report, error) {
	binding, err := e.binder.Bind(ctx, edge)
	if err != nil {
		return Report{}, fmt.Errorf("bind edge %s: %w", edge.ID, err)
	}

	report, err := e.tearDown(ctx, binding, reason)
	if err != nil {
		return report, err
	}

	if err := e.deleteEdgeRow(ctx, edge); err != nil {
		return report, err
	}

	return report, nil
}

// tearDown is the teardown itself, with the reason the caller is acting
// for.
func (e *Engine) tearDown(ctx context.Context, b Binding, reason Reason) (Report, error) {
	mappings, err := e.listMappings(ctx, b)
	if err != nil {
		return Report{}, err
	}

	var (
		report  Report
		errs    []error
		deleted = map[string]bool{}
	)

	for _, mapping := range mappings {
		if err := b.Target.Connector.DeleteEvent(ctx, b.Target.Credential, b.Target.CalendarID, mapping.TargetEventID); err != nil {
			errs = append(errs, fmt.Errorf("edge %s: delete copy %q: %w", b.Edge.ID, mapping.TargetEventID, err))

			continue
		}
		deleted[mapping.TargetEventID] = true
		report.add(Operation{
			SourceEventID: mapping.SourceEventID,
			TargetEventID: mapping.TargetEventID,
			Action:        ActionDelete,
			Reason:        reason,
		})
	}

	// A copy written while the mapping row was not yet recorded carries
	// the marker and nothing else. The promise is that the edge gives back
	// everything it wrote, so the target is swept for the marker as well.
	swept, err := e.sweepMarked(ctx, b, deleted, reason)
	report.merge(swept)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return report, errors.Join(errs...)
	}

	if err := e.scope.WithUser(ctx, b.Edge.UserID, func(ctx context.Context) error {
		_, err := e.mappings.DeleteByEdge(ctx, b.Edge.ID)

		return err
	}); err != nil {
		return report, fmt.Errorf("edge %s: forget mappings: %w", b.Edge.ID, err)
	}

	removed, err := e.removeManagedCalendar(ctx, b)
	if err != nil {
		return report, err
	}
	report.CalendarDeleted = removed

	return report, nil
}

// sweepMarked deletes the events in the target carrying this edge's
// marker that the mapping rows did not already account for.
func (e *Engine) sweepMarked(ctx context.Context, b Binding, deleted map[string]bool, reason Reason) (Report, error) {
	targetState, err := snapshot(ctx, b.Target)
	if err != nil {
		return Report{}, fmt.Errorf("edge %s: read target calendar: %w", b.Edge.ID, err)
	}

	var (
		report Report
		errs   []error
	)

	for _, id := range sortedKeys(targetState) {
		event := targetState[id]
		if deleted[id] || !WrittenBy(event, b.Edge.ID) {
			continue
		}

		if err := b.Target.Connector.DeleteEvent(ctx, b.Target.Credential, b.Target.CalendarID, id); err != nil {
			errs = append(errs, fmt.Errorf("edge %s: delete copy %q: %w", b.Edge.ID, id, err))

			continue
		}
		report.add(Operation{SourceEventID: event.Marker.Source, TargetEventID: id, Action: ActionDelete, Reason: reason})
	}

	return report, errors.Join(errs...)
}

// removeManagedCalendar deletes the target calendar where calbridge
// created it. A calendar the user picked for the edge is never removed,
// and neither is one whose connector cannot create a calendar in the
// first place.
func (e *Engine) removeManagedCalendar(ctx context.Context, b Binding) (bool, error) {
	if !b.Target.Managed || !b.Target.Connector.Capabilities().CalendarCreation {
		return false, nil
	}

	if err := b.Target.Connector.DeleteCalendar(ctx, b.Target.Credential, b.Target.CalendarID); err != nil {
		return false, fmt.Errorf("edge %s: delete managed calendar %q: %w", b.Edge.ID, b.Target.CalendarID, err)
	}

	return true, nil
}

// deleteEdgeRow removes the edge itself, treating an edge somebody else
// already deleted as done.
func (e *Engine) deleteEdgeRow(ctx context.Context, edge storage.Edge) error {
	err := e.scope.WithUser(ctx, edge.UserID, func(ctx context.Context) error {
		return e.edges.Delete(ctx, edge.ID)
	})
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("delete edge %s: %w", edge.ID, err)
	}

	return nil
}

// CheckEdge is what admits an edge before it is stored: it refuses one
// that closes a loop, and one over a calendar its user neither owns nor
// holds a wide enough grant for.
func (e *Engine) CheckEdge(ctx context.Context, edge storage.Edge) error {
	var existing []storage.Edge

	if err := e.scope.WithUser(ctx, edge.UserID, func(ctx context.Context) error {
		found, err := e.edges.List(ctx)
		if err != nil {
			return err
		}
		existing = found

		return nil
	}); err != nil {
		return fmt.Errorf("list edges of user %s: %w", edge.UserID, err)
	}

	if err := CheckLoop(existing, edge); err != nil {
		return err
	}

	coverage, err := e.Coverage(ctx, edge)
	if err != nil {
		return err
	}

	return CheckGrants(edge, coverage)
}

// Coverage reads what the edge's own user holds over the two calendars
// the edge names. A calendar the user does not own is invisible to them
// under row level security, so a lookup that finds nothing is the answer
// "not yours", and a grant is the only other way to reach it.
func (e *Engine) Coverage(ctx context.Context, edge storage.Edge) (Coverage, error) {
	var coverage Coverage

	err := e.scope.WithUser(ctx, edge.UserID, func(ctx context.Context) error {
		ownsSource, err := e.owns(ctx, edge.SourceCalendarID)
		if err != nil {
			return err
		}
		ownsTarget, err := e.owns(ctx, edge.TargetCalendarID)
		if err != nil {
			return err
		}
		grants, err := e.grants.List(ctx)
		if err != nil {
			return err
		}

		coverage = Coverage{OwnsSource: ownsSource, OwnsTarget: ownsTarget, Grants: grants}

		return nil
	})
	if err != nil {
		return Coverage{}, fmt.Errorf("read coverage of edge %s: %w", edge.ID, err)
	}

	return coverage, nil
}

// owns reports whether the scoped user owns the calendar.
func (e *Engine) owns(ctx context.Context, calendarID uuid.UUID) (bool, error) {
	_, err := e.calendars.FindByID(ctx, calendarID)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read calendar %s: %w", calendarID, err)
	}

	return true, nil
}
