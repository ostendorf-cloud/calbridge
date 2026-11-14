// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import "fmt"

// Action is what the engine did to the target for one source event.
type Action uint8

const (
	// ActionNone means nothing was written. Operation.Reason says why,
	// and the saving only exists because the connector was never called.
	ActionNone Action = iota
	// ActionCreate means an event was created in the target.
	ActionCreate
	// ActionUpdate means an event in the target was overwritten.
	ActionUpdate
	// ActionDelete means an event was removed from the target.
	ActionDelete
)

// String returns the action's name.
func (a Action) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
	default:
		return fmt.Sprintf("Action(%d)", uint8(a))
	}
}

// Reason says why an action was taken, or why none was. It is what a
// test asserts on: "the write was refused" is only worth checking when
// the refusal names the mechanism that refused it.
type Reason uint8

const (
	// ReasonNone is the reason of a first copy, which needs none.
	ReasonNone Reason = iota
	// ReasonOwnWrite means the source event carries calbridge's marker, so
	// it is a copy some edge wrote and never a source change.
	ReasonOwnWrite
	// ReasonUnchanged means the content hash already matches what the
	// mapping row holds, so writing would change nothing.
	ReasonUnchanged
	// ReasonCoveredByMaster means the event is one occurrence of a
	// repeating master this edge copies as a whole.
	ReasonCoveredByMaster
	// ReasonUnmapped means the source event was never copied by this edge,
	// so its deletion leaves nothing to remove.
	ReasonUnmapped
	// ReasonSourceChanged means the source event's content no longer
	// matches the copy.
	ReasonSourceChanged
	// ReasonHandEdited means the copy in the target was changed by
	// somebody, and the source is what the engine restores.
	ReasonHandEdited
	// ReasonRestored means the copy was gone from the target and was
	// written again.
	ReasonRestored
	// ReasonSourceGone means the source event no longer exists, so its
	// copy was removed.
	ReasonSourceGone
	// ReasonOutOfWindow means an expanded occurrence left the rolling
	// window. The series is still there, which is what separates this
	// from ReasonSourceGone when an operator reads the report.
	ReasonOutOfWindow
	// ReasonStrategyChanged means the edge now carries a repeating event
	// the other way, as one master with its rule instead of one event per
	// occurrence or the reverse, so the copies under the old key were
	// removed.
	ReasonStrategyChanged
	// ReasonOrphan means the target held an event carrying this edge's
	// marker that no mapping row claims, so the edge removed it.
	ReasonOrphan
	// ReasonMasterGone means the target held an overridden occurrence
	// carrying this edge's marker with no master of it in the calendar,
	// so the object holding it was removed by the master's id. A target
	// declaring no instance writes accepts no other id for it. The
	// operation names that object, which is what was deleted.
	ReasonMasterGone
	// ReasonEdgeRemoved means the edge was torn down and gave back
	// everything it wrote.
	ReasonEdgeRemoved
	// ReasonGrantRevoked means no grant covers the edge any more, so it
	// was torn down.
	ReasonGrantRevoked
)

// String returns the reason's name.
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonOwnWrite:
		return "own write"
	case ReasonUnchanged:
		return "unchanged"
	case ReasonCoveredByMaster:
		return "covered by master"
	case ReasonUnmapped:
		return "unmapped"
	case ReasonSourceChanged:
		return "source changed"
	case ReasonHandEdited:
		return "hand edited"
	case ReasonRestored:
		return "restored"
	case ReasonSourceGone:
		return "source gone"
	case ReasonOutOfWindow:
		return "out of window"
	case ReasonStrategyChanged:
		return "strategy changed"
	case ReasonOrphan:
		return "orphan"
	case ReasonMasterGone:
		return "master gone"
	case ReasonEdgeRemoved:
		return "edge removed"
	case ReasonGrantRevoked:
		return "grant revoked"
	default:
		return fmt.Sprintf("Reason(%d)", uint8(r))
	}
}

// Operation is one decision the engine took, and why.
type Operation struct {
	// SourceEventID is the source event the decision was about. For an
	// expanded occurrence it is the id the occurrence is mapped under, not
	// the master's own id.
	SourceEventID string
	// TargetEventID is the event in the target, empty where none was
	// written.
	TargetEventID string
	// Action is what happened.
	Action Action
	// Reason is why.
	Reason Reason
}

// String renders the operation for a log line or a test failure.
func (o Operation) String() string {
	return fmt.Sprintf("%s %q as %q (%s)", o.Action, o.SourceEventID, o.TargetEventID, o.Reason)
}

// Report is what one call did, in the order it did it.
type Report struct {
	// Operations are the decisions taken, oldest first.
	Operations []Operation
	// CalendarDeleted reports that the target calendar itself was removed,
	// which only happens for a calendar calbridge created.
	CalendarDeleted bool
}

// add appends operations to the report.
func (r *Report) add(ops ...Operation) {
	r.Operations = append(r.Operations, ops...)
}

// merge folds another report into this one.
func (r *Report) merge(other Report) {
	r.Operations = append(r.Operations, other.Operations...)
	r.CalendarDeleted = r.CalendarDeleted || other.CalendarDeleted
}

// Count returns how many operations took the action.
func (r Report) Count(action Action) int {
	n := 0
	for _, op := range r.Operations {
		if op.Action == action {
			n++
		}
	}

	return n
}

// CountReason returns how many operations carry the reason.
func (r Report) CountReason(reason Reason) int {
	n := 0
	for _, op := range r.Operations {
		if op.Reason == reason {
			n++
		}
	}

	return n
}

// reportOf is one operation as a report.
func reportOf(ops ...Operation) Report {
	return Report{Operations: ops}
}
