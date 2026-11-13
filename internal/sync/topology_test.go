// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

func TestCheckLoop(t *testing.T) {
	t.Parallel()

	user := uuid.New()
	privat, arbeit, studium := uuid.New(), uuid.New(), uuid.New()

	edge := func(source, target uuid.UUID) storage.Edge {
		return storage.Edge{
			ID:               uuid.New(),
			UserID:           user,
			SourceCalendarID: source,
			TargetCalendarID: target,
			DetailLevel:      storage.DetailLevelBusyBlock,
		}
	}

	tests := []struct {
		name      string
		existing  []storage.Edge
		candidate storage.Edge
		wantErr   error
	}{
		{
			name:      "the first edge between two calendars",
			candidate: edge(privat, arbeit),
		},
		{
			name:      "a second edge out of the same source",
			existing:  []storage.Edge{edge(privat, arbeit)},
			candidate: edge(privat, studium),
		},
		{
			name:      "two edges into the same target",
			existing:  []storage.Edge{edge(privat, arbeit)},
			candidate: edge(studium, arbeit),
		},
		{
			name:      "the opposing edge",
			existing:  []storage.Edge{edge(privat, arbeit)},
			candidate: edge(arbeit, privat),
			wantErr:   sync.ErrLoop,
		},
		{
			name:      "a ring through a third calendar",
			existing:  []storage.Edge{edge(privat, arbeit), edge(arbeit, studium)},
			candidate: edge(studium, privat),
			wantErr:   sync.ErrLoop,
		},
		{
			name:      "a calendar onto itself",
			candidate: edge(privat, privat),
			wantErr:   sync.ErrLoop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := sync.CheckLoop(tt.existing, tt.candidate)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckLoop_doesNotReportAStoredEdgeAgainstItself(t *testing.T) {
	t.Parallel()

	stored := storage.Edge{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		SourceCalendarID: uuid.New(),
		TargetCalendarID: uuid.New(),
		DetailLevel:      storage.DetailLevelFull,
	}

	if err := sync.CheckLoop([]storage.Edge{stored}, stored); err != nil {
		t.Fatalf("got %v, want the edge admitted", err)
	}
}

func TestCheckGrants(t *testing.T) {
	t.Parallel()

	owner, grantee := uuid.New(), uuid.New()
	shared, own := uuid.New(), uuid.New()

	edge := storage.Edge{
		ID:               uuid.New(),
		UserID:           grantee,
		SourceCalendarID: shared,
		TargetCalendarID: own,
		DetailLevel:      storage.DetailLevelTitleTime,
	}

	grant := func(level storage.DetailLevel, calendarID, granteeID uuid.UUID) storage.CalendarGrant {
		return storage.CalendarGrant{
			ID:             uuid.New(),
			OwnerUserID:    owner,
			GranteeUserID:  granteeID,
			CalendarID:     calendarID,
			MaxDetailLevel: level,
		}
	}

	tests := []struct {
		name     string
		coverage sync.Coverage
		wantErr  error
	}{
		{
			name:     "both calendars owned outright",
			coverage: sync.Coverage{OwnsSource: true, OwnsTarget: true},
		},
		{
			name: "a grant at exactly the edge's level",
			coverage: sync.Coverage{
				OwnsTarget: true,
				Grants:     []storage.CalendarGrant{grant(storage.DetailLevelTitleTime, shared, grantee)},
			},
		},
		{
			name: "a grant wider than the edge",
			coverage: sync.Coverage{
				OwnsTarget: true,
				Grants:     []storage.CalendarGrant{grant(storage.DetailLevelFull, shared, grantee)},
			},
		},
		{
			name:     "no grant at all",
			coverage: sync.Coverage{OwnsTarget: true},
			wantErr:  sync.ErrGrantMissing,
		},
		{
			name: "a grant to somebody else",
			coverage: sync.Coverage{
				OwnsTarget: true,
				Grants:     []storage.CalendarGrant{grant(storage.DetailLevelFull, shared, uuid.New())},
			},
			wantErr: sync.ErrGrantMissing,
		},
		{
			name: "a grant over another calendar",
			coverage: sync.Coverage{
				OwnsTarget: true,
				Grants:     []storage.CalendarGrant{grant(storage.DetailLevelFull, uuid.New(), grantee)},
			},
			wantErr: sync.ErrGrantMissing,
		},
		{
			name: "a grant narrower than the edge",
			coverage: sync.Coverage{
				OwnsTarget: true,
				Grants:     []storage.CalendarGrant{grant(storage.DetailLevelBusyBlock, shared, grantee)},
			},
			wantErr: sync.ErrGrantExceeded,
		},
		{
			name: "the target is the calendar nobody shared",
			coverage: sync.Coverage{
				OwnsSource: true,
				Grants:     []storage.CalendarGrant{grant(storage.DetailLevelFull, shared, grantee)},
			},
			wantErr: sync.ErrGrantMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := sync.CheckGrants(edge, tt.coverage)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckGrants_refusesADetailLevelNobodyDefined(t *testing.T) {
	t.Parallel()

	edge := storage.Edge{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		SourceCalendarID: uuid.New(),
		TargetCalendarID: uuid.New(),
		DetailLevel:      storage.DetailLevel("everything"),
	}

	err := sync.CheckGrants(edge, sync.Coverage{OwnsSource: true, OwnsTarget: true})
	if !errors.Is(err, sync.ErrInvalidDetailLevel) {
		t.Fatalf("got %v, want %v", err, sync.ErrInvalidDetailLevel)
	}
}
