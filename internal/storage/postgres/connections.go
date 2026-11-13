// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// connectionRepository satisfies storage.ConnectionRepository. Every
// method requires a context storage.UserScope.WithUser produced; see
// txFromContext. encryptor encrypts Credentials before every write and
// decrypts it after every read, so storage.Connection.Credentials is
// always in clear on either side of this package; see crypto.go.
type connectionRepository struct {
	encryptor *encryptor
}

// Create implements storage.ConnectionRepository.
func (r *connectionRepository) Create(ctx context.Context, connection storage.Connection) (storage.Connection, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Connection{}, err
	}

	encrypted, err := r.encryptor.encrypt(connection.Credentials)
	if err != nil {
		return storage.Connection{}, fmt.Errorf("encrypt credentials: %w", err)
	}

	row, err := sqlcgen.New(tx).CreateConnection(ctx, sqlcgen.CreateConnectionParams{
		UserID:      connection.UserID,
		Provider:    string(connection.Provider),
		DisplayName: connection.DisplayName,
		Credentials: encrypted,
	})
	if err != nil {
		return storage.Connection{}, mapWriteError(err)
	}

	return r.connectionFromRow(row)
}

// FindByID implements storage.ConnectionRepository.
func (r *connectionRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.Connection, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Connection{}, err
	}

	row, err := sqlcgen.New(tx).GetConnectionByID(ctx, id)
	if err != nil {
		return storage.Connection{}, mapReadError(err)
	}

	return r.connectionFromRow(row)
}

// List implements storage.ConnectionRepository.
func (r *connectionRepository) List(ctx context.Context) ([]storage.Connection, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListConnections(ctx)
	if err != nil {
		return nil, mapReadError(err)
	}

	connections := make([]storage.Connection, len(rows))
	for i, row := range rows {
		connection, err := r.connectionFromRow(row)
		if err != nil {
			return nil, err
		}

		connections[i] = connection
	}

	return connections, nil
}

// UpdateCredentials implements storage.ConnectionRepository.
func (r *connectionRepository) UpdateCredentials(ctx context.Context, id uuid.UUID, credentials []byte) (storage.Connection, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.Connection{}, err
	}

	encrypted, err := r.encryptor.encrypt(credentials)
	if err != nil {
		return storage.Connection{}, fmt.Errorf("encrypt credentials: %w", err)
	}

	row, err := sqlcgen.New(tx).UpdateConnectionCredentials(ctx, sqlcgen.UpdateConnectionCredentialsParams{
		ID:          id,
		Credentials: encrypted,
	})
	if err != nil {
		return storage.Connection{}, mapWriteError(err)
	}

	return r.connectionFromRow(row)
}

// Delete implements storage.ConnectionRepository.
func (r *connectionRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := txFromContext(ctx)
	if err != nil {
		return err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteConnection(ctx, id)
	if err != nil {
		return mapWriteError(err)
	}

	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

func (r *connectionRepository) connectionFromRow(row sqlcgen.Connection) (storage.Connection, error) {
	credentials, err := r.encryptor.decrypt(row.Credentials)
	if err != nil {
		return storage.Connection{}, fmt.Errorf("decrypt credentials: %w", err)
	}

	return storage.Connection{
		ID:          row.ID,
		UserID:      row.UserID,
		Provider:    storage.ConnectionProvider(row.Provider),
		DisplayName: row.DisplayName,
		Credentials: credentials,
		CreatedAt:   timeFromTimestamptz(row.CreatedAt),
		UpdatedAt:   timeFromTimestamptz(row.UpdatedAt),
	}, nil
}
