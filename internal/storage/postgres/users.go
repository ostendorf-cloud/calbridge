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

// userRepository satisfies storage.UserRepository. It runs directly
// against the pool, never against a WithUser-scoped transaction: users
// carries no row level security to scope by, see db/migrations.
type userRepository struct {
	pool *pgxpool.Pool
}

// Create implements storage.UserRepository.
func (r *userRepository) Create(ctx context.Context, user storage.User) (storage.User, error) {
	row, err := sqlcgen.New(r.pool).CreateUser(ctx, sqlcgen.CreateUserParams{
		Email:           user.Email,
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

// FindByID implements storage.UserRepository.
func (r *userRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.User, error) {
	row, err := sqlcgen.New(r.pool).GetUserByID(ctx, id)
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

// Update implements storage.UserRepository.
func (r *userRepository) Update(ctx context.Context, user storage.User) (storage.User, error) {
	row, err := sqlcgen.New(r.pool).UpdateUser(ctx, sqlcgen.UpdateUserParams{
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
