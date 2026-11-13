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

// identityRepository satisfies storage.IdentityRepository. Every method
// except FindByProviderSubject requires a context
// storage.UserScope.WithUser produced; see txFromContext.
// FindByProviderSubject runs against the pool instead, the same
// exception userRepository.FindByEmail is and for the same reason; see
// storage.IdentityRepository.
type identityRepository struct {
	pool *pgxpool.Pool
}

// Create implements storage.IdentityRepository.
func (r *identityRepository) Create(ctx context.Context, identity storage.Identity) (storage.Identity, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Identity{}, err
	}

	row, err := sqlcgen.New(tx).CreateIdentity(ctx, sqlcgen.CreateIdentityParams{
		UserID:   identity.UserID,
		Provider: string(identity.Provider),
		Subject:  identity.Subject,
	})
	if err != nil {
		return storage.Identity{}, mapWriteError(err)
	}

	return identityFromRow(row), nil
}

// FindByID implements storage.IdentityRepository.
func (r *identityRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.Identity, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Identity{}, err
	}

	row, err := sqlcgen.New(tx).GetIdentityByID(ctx, id)
	if err != nil {
		return storage.Identity{}, mapReadError(err)
	}

	return identityFromRow(row), nil
}

// FindByProviderSubject implements storage.IdentityRepository. It is not
// user-scoped: see that interface's own doc comment, and db/migrations
// for the SECURITY DEFINER function behind it.
func (r *identityRepository) FindByProviderSubject(
	ctx context.Context,
	provider storage.IdentityProvider,
	subject string,
) (storage.Identity, error) {
	row, err := sqlcgen.New(r.pool).GetIdentityByProviderSubject(ctx, sqlcgen.GetIdentityByProviderSubjectParams{
		Provider: string(provider),
		Subject:  subject,
	})
	if err != nil {
		return storage.Identity{}, mapReadError(err)
	}

	return identityFromRow(row), nil
}

// List implements storage.IdentityRepository.
func (r *identityRepository) List(ctx context.Context) ([]storage.Identity, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListIdentities(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	identities := make([]storage.Identity, len(rows))
	for i, row := range rows {
		identities[i] = identityFromRow(row)
	}

	return identities, nil
}

// Delete implements storage.IdentityRepository.
func (r *identityRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := txFromContext(ctx)
	if err != nil {
		return err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteIdentity(ctx, id)
	if err != nil {
		return mapWriteError(err)
	}

	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

func identityFromRow(row sqlcgen.Identity) storage.Identity {
	return storage.Identity{
		ID:        row.ID,
		UserID:    row.UserID,
		Provider:  storage.IdentityProvider(row.Provider),
		Subject:   row.Subject,
		CreatedAt: timeFromTimestamptz(row.CreatedAt),
	}
}
