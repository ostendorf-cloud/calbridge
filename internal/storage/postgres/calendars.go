// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// calendarRepository satisfies storage.CalendarRepository. Every method
// requires a context storage.UserScope.WithUser produced; see
// txFromContext.
type calendarRepository struct{}

// Create implements storage.CalendarRepository.
func (r *calendarRepository) Create(ctx context.Context, calendar storage.Calendar) (storage.Calendar, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Calendar{}, err
	}

	row, err := sqlcgen.New(tx).CreateCalendar(ctx, sqlcgen.CreateCalendarParams{
		UserID:       calendar.UserID,
		ConnectionID: calendar.ConnectionID,
		ExternalID:   calendar.ExternalID,
		Name:         calendar.Name,
		Managed:      calendar.Managed,
	})
	if err != nil {
		return storage.Calendar{}, mapWriteError(err)
	}

	return calendarFromRow(row), nil
}

// FindByID implements storage.CalendarRepository.
func (r *calendarRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.Calendar, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Calendar{}, err
	}

	row, err := sqlcgen.New(tx).GetCalendarByID(ctx, id)
	if err != nil {
		return storage.Calendar{}, mapReadError(err)
	}

	return calendarFromRow(row), nil
}

// List implements storage.CalendarRepository.
func (r *calendarRepository) List(ctx context.Context) ([]storage.Calendar, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListCalendars(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	calendars := make([]storage.Calendar, len(rows))
	for i, row := range rows {
		calendars[i] = calendarFromRow(row)
	}

	return calendars, nil
}

// Delete implements storage.CalendarRepository.
func (r *calendarRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := txFromContext(ctx)
	if err != nil {
		return err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteCalendar(ctx, id)
	if err != nil {
		return mapWriteError(err)
	}

	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

func calendarFromRow(row sqlcgen.Calendar) storage.Calendar {
	return storage.Calendar{
		ID:           row.ID,
		UserID:       row.UserID,
		ConnectionID: row.ConnectionID,
		ExternalID:   row.ExternalID,
		Name:         row.Name,
		Managed:      row.Managed,
		CreatedAt:    timeFromTimestamptz(row.CreatedAt),
		UpdatedAt:    timeFromTimestamptz(row.UpdatedAt),
	}
}
