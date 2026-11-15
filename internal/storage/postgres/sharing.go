// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// sharingRepository satisfies storage.SharingRepository: everything in
// this package that reaches across the boundary between one user and
// another, in one file, because that is the surface worth being able to
// read at once.
//
// Each of the three reads is bounded by something the schema itself
// decides, never by a filter written here:
//
//   - ListCalendarsSharedWithMe joins calendar_grants, and
//     calendars_grantee_read is the policy that admits a row at all.
//   - ListGrantCounterparties goes through grant_counterparties, a
//     SECURITY DEFINER function that scopes itself to app.user_id and
//     takes no parameter, so there is nothing a caller could ask with.
//   - FindUserIDByEmail goes through users_find_by_email, the same
//     function the login lookup uses, and projects the id alone.
//
// pool is set because FindUserIDByEmail runs outside a transaction, the
// same arrangement userRepository already has for FindByEmail; the other
// two take their connection from the scoped transaction on the context.
type sharingRepository struct {
	pool *pgxpool.Pool
}

// ListCalendarsSharedWithMe implements storage.SharingRepository. It is
// the one read in this package that returns a calendar under somebody
// else's user id, and the grant it comes back with is what makes that
// safe to hold; see storage.SharedCalendar.
func (r *sharingRepository) ListCalendarsSharedWithMe(
	ctx context.Context,
) ([]storage.SharedCalendar, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListCalendarsSharedWithMe(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	shared := make([]storage.SharedCalendar, len(rows))
	for i, row := range rows {
		shared[i] = storage.SharedCalendar{
			ID:             row.ID,
			UserID:         row.UserID,
			ConnectionID:   row.ConnectionID,
			Name:           row.Name,
			Managed:        row.Managed,
			CreatedAt:      timeFromTimestamptz(row.CreatedAt),
			UpdatedAt:      timeFromTimestamptz(row.UpdatedAt),
			GrantID:        row.GrantID,
			MaxDetailLevel: storage.DetailLevel(row.MaxDetailLevel),
		}
	}

	return shared, nil
}

// ListGrantCounterparties implements storage.SharingRepository.
func (r *sharingRepository) ListGrantCounterparties(
	ctx context.Context,
) ([]storage.GrantCounterparty, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListGrantCounterparties(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	counterparties := make([]storage.GrantCounterparty, len(rows))
	for i, row := range rows {
		counterparties[i] = storage.GrantCounterparty{UserID: row.ID, Email: row.Email}
	}

	return counterparties, nil
}

// FindUserIDByEmail implements storage.SharingRepository. It runs
// against the pool directly, for the same reason
// userRepository.FindByEmail does: it answers for an address that is not
// the caller's own, so no scope it could run inside would admit the row.
// The query projects the id alone, so the hash and the TOTP secret the
// function returns never leave the database; see queries/users.sql.
func (r *sharingRepository) FindUserIDByEmail(ctx context.Context, email string) (uuid.UUID, error) {
	id, err := sqlcgen.New(r.pool).GetUserIDByEmail(ctx, email)
	if err != nil {
		return uuid.Nil, mapReadError(err)
	}

	return id, nil
}
