// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// calendarGrantRepository satisfies storage.CalendarGrantRepository.
// Every method requires a context storage.UserScope.WithUser produced;
// see txFromContext. Backed by the calendar_grants table, named that
// rather than the shorter "grants" the domain type uses because GRANT is
// a reserved word in PostgreSQL's own grammar; see db/migrations.
type calendarGrantRepository struct{}

// Create implements storage.CalendarGrantRepository.
func (r *calendarGrantRepository) Create(ctx context.Context, grant storage.CalendarGrant) (storage.CalendarGrant, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.CalendarGrant{}, err
	}

	row, err := sqlcgen.New(tx).CreateCalendarGrant(ctx, sqlcgen.CreateCalendarGrantParams{
		OwnerUserID:    grant.OwnerUserID,
		GranteeUserID:  grant.GranteeUserID,
		CalendarID:     grant.CalendarID,
		MaxDetailLevel: string(grant.MaxDetailLevel),
	})
	if err != nil {
		return storage.CalendarGrant{}, mapWriteError(err)
	}

	return calendarGrantFromRow(row), nil
}

// FindByID implements storage.CalendarGrantRepository.
func (r *calendarGrantRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.CalendarGrant, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.CalendarGrant{}, err
	}

	row, err := sqlcgen.New(tx).GetCalendarGrantByID(ctx, id)
	if err != nil {
		return storage.CalendarGrant{}, mapReadError(err)
	}

	return calendarGrantFromRow(row), nil
}

// List implements storage.CalendarGrantRepository.
func (r *calendarGrantRepository) List(ctx context.Context) ([]storage.CalendarGrant, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListCalendarGrants(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	grants := make([]storage.CalendarGrant, len(rows))
	for i, row := range rows {
		grants[i] = calendarGrantFromRow(row)
	}

	return grants, nil
}

// Delete implements storage.CalendarGrantRepository.
func (r *calendarGrantRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := txFromContext(ctx)
	if err != nil {
		return err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteCalendarGrant(ctx, id)
	if err != nil {
		return mapWriteError(err)
	}

	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

func calendarGrantFromRow(row sqlcgen.CalendarGrant) storage.CalendarGrant {
	return storage.CalendarGrant{
		ID:             row.ID,
		OwnerUserID:    row.OwnerUserID,
		GranteeUserID:  row.GranteeUserID,
		CalendarID:     row.CalendarID,
		MaxDetailLevel: storage.DetailLevel(row.MaxDetailLevel),
		CreatedAt:      timeFromTimestamptz(row.CreatedAt),
	}
}
