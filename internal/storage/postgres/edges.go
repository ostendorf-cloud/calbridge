// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// edgeRepository satisfies storage.EdgeRepository. Every method requires
// a context storage.UserScope.WithUser produced; see txFromContext.
type edgeRepository struct{}

// Create implements storage.EdgeRepository.
func (r *edgeRepository) Create(ctx context.Context, edge storage.Edge) (storage.Edge, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Edge{}, err
	}

	row, err := sqlcgen.New(tx).CreateEdge(ctx, sqlcgen.CreateEdgeParams{
		UserID:           edge.UserID,
		SourceCalendarID: edge.SourceCalendarID,
		TargetCalendarID: edge.TargetCalendarID,
		DetailLevel:      string(edge.DetailLevel),
	})
	if err != nil {
		return storage.Edge{}, mapWriteError(err)
	}

	return edgeFromRow(row), nil
}

// FindByID implements storage.EdgeRepository.
func (r *edgeRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.Edge, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Edge{}, err
	}

	row, err := sqlcgen.New(tx).GetEdgeByID(ctx, id)
	if err != nil {
		return storage.Edge{}, mapReadError(err)
	}

	return edgeFromRow(row), nil
}

// List implements storage.EdgeRepository.
func (r *edgeRepository) List(ctx context.Context) ([]storage.Edge, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListEdges(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	edges := make([]storage.Edge, len(rows))
	for i, row := range rows {
		edges[i] = edgeFromRow(row)
	}

	return edges, nil
}

// ListByCalendar implements storage.EdgeRepository.
func (r *edgeRepository) ListByCalendar(ctx context.Context, calendarID uuid.UUID) ([]storage.Edge, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListEdgesByCalendar(ctx, calendarID)
	if err != nil {
		return nil, mapReadError(err)
	}

	edges := make([]storage.Edge, len(rows))
	for i, row := range rows {
		edges[i] = edgeFromRow(row)
	}

	return edges, nil
}

// UpdateDetailLevel implements storage.EdgeRepository.
func (r *edgeRepository) UpdateDetailLevel(ctx context.Context, id uuid.UUID, detailLevel storage.DetailLevel) (storage.Edge, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Edge{}, err
	}

	row, err := sqlcgen.New(tx).UpdateEdgeDetailLevel(ctx, sqlcgen.UpdateEdgeDetailLevelParams{
		ID:          id,
		DetailLevel: string(detailLevel),
	})
	if err != nil {
		return storage.Edge{}, mapWriteError(err)
	}

	return edgeFromRow(row), nil
}

// Delete implements storage.EdgeRepository.
func (r *edgeRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := txFromContext(ctx)
	if err != nil {
		return err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteEdge(ctx, id)
	if err != nil {
		return mapWriteError(err)
	}

	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

func edgeFromRow(row sqlcgen.Edge) storage.Edge {
	return storage.Edge{
		ID:               row.ID,
		UserID:           row.UserID,
		SourceCalendarID: row.SourceCalendarID,
		TargetCalendarID: row.TargetCalendarID,
		DetailLevel:      storage.DetailLevel(row.DetailLevel),
		CreatedAt:        timeFromTimestamptz(row.CreatedAt),
		UpdatedAt:        timeFromTimestamptz(row.UpdatedAt),
	}
}
