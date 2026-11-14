// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// CheckLoop refuses an edge whose target calendar already reaches its
// source calendar. Two opposing edges are the shortest case of that, and
// a longer ring through a third calendar is the same mistake with more
// steps, so both are refused by the same walk.
//
// existing is the set of edges the candidate joins. The candidate's own
// id is ignored inside it, so re-checking a stored edge does not report
// the edge against itself.
func CheckLoop(existing []storage.Edge, candidate storage.Edge) error {
	if candidate.SourceCalendarID == candidate.TargetCalendarID {
		return fmt.Errorf("edge copies calendar %s onto itself: %w", candidate.SourceCalendarID, ErrLoop)
	}

	outgoing := map[uuid.UUID][]storage.Edge{}
	for _, edge := range existing {
		if edge.ID == candidate.ID {
			continue
		}
		outgoing[edge.SourceCalendarID] = append(outgoing[edge.SourceCalendarID], edge)
	}

	seen := map[uuid.UUID]bool{candidate.TargetCalendarID: true}
	queue := []uuid.UUID{candidate.TargetCalendarID}

	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]

		for _, edge := range outgoing[at] {
			if edge.TargetCalendarID == candidate.SourceCalendarID {
				return fmt.Errorf("edge %s already copies calendar %s back to %s: %w",
					edge.ID, candidate.TargetCalendarID, candidate.SourceCalendarID, ErrLoop)
			}
			if seen[edge.TargetCalendarID] {
				continue
			}
			seen[edge.TargetCalendarID] = true
			queue = append(queue, edge.TargetCalendarID)
		}
	}

	return nil
}

// Coverage is what the edge's own user holds over the two calendars the
// edge names: whether they own each of them outright, and the grants
// visible to them.
type Coverage struct {
	// OwnsSource reports that the edge's user owns the source calendar.
	OwnsSource bool
	// OwnsTarget reports that the edge's user owns the target calendar.
	OwnsTarget bool
	// Grants are the calendar grants the edge's user can see, from either
	// side.
	Grants []storage.CalendarGrant
}

// CheckGrants refuses an edge over a calendar its user neither owns nor
// holds a grant for, and an edge copying more detail than that grant
// allows. The database refuses the first of those when an edge is
// written; revoking the grant afterwards does not touch the edge, so the
// same check runs again on every pass.
func CheckGrants(edge storage.Edge, coverage Coverage) error {
	level, err := MaskFor(edge.DetailLevel)
	if err != nil {
		return fmt.Errorf("edge %s: %w", edge.ID, err)
	}

	sides := []struct {
		name       string
		calendarID uuid.UUID
		owned      bool
	}{
		{"source", edge.SourceCalendarID, coverage.OwnsSource},
		{"target", edge.TargetCalendarID, coverage.OwnsTarget},
	}

	for _, side := range sides {
		if side.owned {
			continue
		}

		grant, found := findGrant(coverage.Grants, side.calendarID, edge.UserID)
		if !found {
			return fmt.Errorf("edge %s names %s calendar %s: %w", edge.ID, side.name, side.calendarID, ErrGrantMissing)
		}

		allowed, err := MaskFor(grant.MaxDetailLevel)
		if err != nil {
			return fmt.Errorf("grant %s: %w", grant.ID, err)
		}
		if !level.Within(allowed) {
			return fmt.Errorf("edge %s copies %s, grant %s allows %s: %w", edge.ID, level, grant.ID, allowed, ErrGrantExceeded)
		}
	}

	return nil
}

// findGrant returns the grant letting granteeID reach calendarID.
func findGrant(grants []storage.CalendarGrant, calendarID, granteeID uuid.UUID) (storage.CalendarGrant, bool) {
	for _, grant := range grants {
		if grant.CalendarID == calendarID && grant.GranteeUserID == granteeID {
			return grant, true
		}
	}

	return storage.CalendarGrant{}, false
}
