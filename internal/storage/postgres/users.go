// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// userRepository satisfies storage.UserRepository. FindByEmail is the
// only method that runs directly against the pool: the email lookup has
// to work before any user id is known at all, through
// users_find_by_email, a SECURITY DEFINER function; see db/migrations.
// Create, FindByID and Update all run inside a transaction
// storage.UserScope.WithUser scoped, like every other repository, now
// that users carries row level security of its own. Create is the one
// that scopes that transaction itself, to the user it is about to
// create; its caller has no id to pass yet.
type userRepository struct {
	pool  *pgxpool.Pool
	scope storage.UserScope
}

// Create implements storage.UserRepository. It generates the new user's
// id and runs the INSERT in a transaction scoped to exactly that id,
// which is what lets signup work for a user who does not exist yet
// without exempting users from its own policy: the row written and the
// row RETURNING reads back are both the row app.user_id names, so
// users_self_isolation accepts both, and nothing else in the same
// transaction becomes reachable. Any id on user is ignored; the created
// user carries the generated one.
func (r *userRepository) Create(ctx context.Context, user storage.User) (storage.User, error) {
	id := uuid.New()

	var created storage.User

	err := r.scope.WithUser(ctx, id, func(ctx context.Context) error {
		tx, err := txFromContext(ctx)
		if err != nil {
			return err
		}

		row, err := sqlcgen.New(tx).CreateUser(ctx, sqlcgen.CreateUserParams{
			ID:              id,
			Email:           user.Email,
			PasswordHash:    ptrToText(user.PasswordHash),
			TotpSecret:      ptrToText(user.TOTPSecret),
			TotpLastCounter: ptrToInt8(user.TOTPLastCounter),
			Status:          string(user.Status),
		})
		if err != nil {
			return mapWriteError(err)
		}

		created = userFromRow(row)

		return nil
	})
	if err != nil {
		return storage.User{}, err
	}

	return created, nil
}

// FindByID implements storage.UserRepository. It requires a context
// storage.UserScope.WithUser produced; see storage.ErrNoUserScope.
func (r *userRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.User, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.User{}, err
	}

	row, err := sqlcgen.New(tx).GetUserByID(ctx, id)
	if err != nil {
		return storage.User{}, mapReadError(err)
	}

	return userFromRow(row), nil
}

// FindByEmail implements storage.UserRepository.
func (r *userRepository) FindByEmail(ctx context.Context, email string) (storage.User, error) {
	row, err := sqlcgen.New(r.pool).GetUserByEmail(ctx, email)
	if err != nil {
		return storage.User{}, mapReadError(err)
	}

	return userFromRow(row), nil
}

// Update implements storage.UserRepository. It requires a context
// storage.UserScope.WithUser produced; see storage.ErrNoUserScope.
func (r *userRepository) Update(ctx context.Context, user storage.User) (storage.User, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.User{}, err
	}

	row, err := sqlcgen.New(tx).UpdateUser(ctx, sqlcgen.UpdateUserParams{
		ID:              user.ID,
		PasswordHash:    ptrToText(user.PasswordHash),
		TotpSecret:      ptrToText(user.TOTPSecret),
		TotpLastCounter: ptrToInt8(user.TOTPLastCounter),
		Status:          string(user.Status),
	})
	if err != nil {
		return storage.User{}, mapWriteError(err)
	}

	return userFromRow(row), nil
}

// DeleteAccount implements storage.UserRepository. Like Create, it
// scopes its own transaction, here to the user it is about to remove:
// its caller is ending that user's own account and has no other scope to
// run inside. Every statement below is bounded by that scope and by row
// level security, so none of them can reach a row belonging to anybody
// else even though none of them names a user id of its own.
//
// The order is the contract: no table in this schema carries ON DELETE
// CASCADE, so a row only ever goes when a statement says so, leaf first.
// queries/account_deletion.sql holds the statements in exactly this
// order and records why.
//
// One case this does not resolve on its own: an edge another user built
// against a calendar this user granted them still references that
// calendar, and is invisible from this scope, so Postgres refuses the
// calendar delete with a foreign key violation. Cross-user grants are
// issue #12's to build and to clean up; until then no such edge can
// exist, and the refusal is a loud one rather than a silent partial
// delete, since the whole sequence runs in one transaction.
func (r *userRepository) DeleteAccount(ctx context.Context, id uuid.UUID) error {
	return r.scope.WithUser(ctx, id, func(ctx context.Context) error {
		tx, err := txFromContext(ctx)
		if err != nil {
			return err
		}

		q := sqlcgen.New(tx)

		for _, step := range []func(context.Context) (int64, error){
			q.DeleteEventMappingsForUser,
			q.DeleteEdgesForUser,
			q.DeleteCalendarGrantsForUser,
			q.DeleteCalendarsForUser,
			q.DeleteConnectionsForUser,
			q.DeleteIdentitiesForUser,
		} {
			if _, err := step(ctx); err != nil {
				return mapWriteError(err)
			}
		}

		rowsAffected, err := q.DeleteUser(ctx)
		if err != nil {
			return mapWriteError(err)
		}

		if rowsAffected == 0 {
			return storage.ErrNotFound
		}

		return nil
	})
}

func userFromRow(row sqlcgen.User) storage.User {
	return storage.User{
		ID:              row.ID,
		Email:           row.Email,
		PasswordHash:    textToPtr(row.PasswordHash),
		TOTPSecret:      textToPtr(row.TotpSecret),
		TOTPLastCounter: int8ToPtr(row.TotpLastCounter),
		Status:          storage.UserStatus(row.Status),
		CreatedAt:       timeFromTimestamptz(row.CreatedAt),
		UpdatedAt:       timeFromTimestamptz(row.UpdatedAt),
	}
}
