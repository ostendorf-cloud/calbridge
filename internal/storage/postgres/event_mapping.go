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

// eventMappingRepository satisfies storage.EventMappingRepository. Every
// method requires a context storage.UserScope.WithUser produced; see
// txFromContext. encryptor encrypts Details before every write and
// decrypts it after every read, so storage.EventMapping.Details is
// always in clear on either side of this package; see crypto.go.
type eventMappingRepository struct {
	encryptor *encryptor
}

// Create implements storage.EventMappingRepository.
func (r *eventMappingRepository) Create(ctx context.Context, mapping storage.EventMapping) (storage.EventMapping, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	encrypted, err := r.encryptor.encrypt(mapping.Details)
	if err != nil {
		return storage.EventMapping{}, fmt.Errorf("encrypt details: %w", err)
	}

	row, err := sqlcgen.New(tx).CreateEventMapping(ctx, sqlcgen.CreateEventMappingParams{
		UserID:           mapping.UserID,
		EdgeID:           mapping.EdgeID,
		SourceEventID:    mapping.SourceEventID,
		TargetEventID:    mapping.TargetEventID,
		StartsAt:         toTimestamptz(mapping.StartsAt),
		EndsAt:           toTimestamptz(mapping.EndsAt),
		ContentHash:      mapping.ContentHash,
		EncryptedDetails: encrypted,
	})
	if err != nil {
		return storage.EventMapping{}, mapWriteError(err)
	}

	return r.eventMappingFromRow(row)
}

// FindByID implements storage.EventMappingRepository.
func (r *eventMappingRepository) FindByID(ctx context.Context, id uuid.UUID) (storage.EventMapping, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	row, err := sqlcgen.New(tx).GetEventMappingByID(ctx, id)
	if err != nil {
		return storage.EventMapping{}, mapReadError(err)
	}

	return r.eventMappingFromRow(row)
}

// FindByEdgeAndSourceEventID implements storage.EventMappingRepository.
func (r *eventMappingRepository) FindByEdgeAndSourceEventID(ctx context.Context, edgeID uuid.UUID, sourceEventID string) (storage.EventMapping, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	row, err := sqlcgen.New(tx).GetEventMappingByEdgeAndSourceEventID(ctx, sqlcgen.GetEventMappingByEdgeAndSourceEventIDParams{
		EdgeID:        edgeID,
		SourceEventID: sourceEventID,
	})
	if err != nil {
		return storage.EventMapping{}, mapReadError(err)
	}

	return r.eventMappingFromRow(row)
}

// ListByEdge implements storage.EventMappingRepository.
func (r *eventMappingRepository) ListByEdge(ctx context.Context, edgeID uuid.UUID) ([]storage.EventMapping, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := sqlcgen.New(tx).ListEventMappingsByEdge(ctx, edgeID)
	if err != nil {
		return nil, mapReadError(err)
	}

	mappings := make([]storage.EventMapping, len(rows))
	for i, row := range rows {
		mappings[i], err = r.eventMappingFromRow(row)
		if err != nil {
			return nil, err
		}
	}

	return mappings, nil
}

// Update implements storage.EventMappingRepository.
func (r *eventMappingRepository) Update(ctx context.Context, mapping storage.EventMapping) (storage.EventMapping, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return storage.EventMapping{}, err
	}

	encrypted, err := r.encryptor.encrypt(mapping.Details)
	if err != nil {
		return storage.EventMapping{}, fmt.Errorf("encrypt details: %w", err)
	}

	row, err := sqlcgen.New(tx).UpdateEventMapping(ctx, sqlcgen.UpdateEventMappingParams{
		ID:               mapping.ID,
		TargetEventID:    mapping.TargetEventID,
		StartsAt:         toTimestamptz(mapping.StartsAt),
		EndsAt:           toTimestamptz(mapping.EndsAt),
		ContentHash:      mapping.ContentHash,
		EncryptedDetails: encrypted,
	})
	if err != nil {
		return storage.EventMapping{}, mapWriteError(err)
	}

	return r.eventMappingFromRow(row)
}

// Delete implements storage.EventMappingRepository.
func (r *eventMappingRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := txFromContext(ctx)
	if err != nil {
		return err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteEventMapping(ctx, id)
	if err != nil {
		return mapWriteError(err)
	}

	if rowsAffected == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// DeleteByEdge implements storage.EventMappingRepository. Removing no
// row is not an error here: an edge that never wrote anything is torn
// down the same way as one that did.
func (r *eventMappingRepository) DeleteByEdge(ctx context.Context, edgeID uuid.UUID) (int64, error) {
	tx, err := txFromContext(ctx)
	if err != nil {
		return 0, err
	}

	rowsAffected, err := sqlcgen.New(tx).DeleteEventMappingsByEdge(ctx, edgeID)
	if err != nil {
		return 0, mapWriteError(err)
	}

	return rowsAffected, nil
}

func (r *eventMappingRepository) eventMappingFromRow(row sqlcgen.EventMapping) (storage.EventMapping, error) {
	details, err := r.encryptor.decrypt(row.EncryptedDetails)
	if err != nil {
		return storage.EventMapping{}, fmt.Errorf("decrypt details: %w", err)
	}

	return storage.EventMapping{
		ID:            row.ID,
		UserID:        row.UserID,
		EdgeID:        row.EdgeID,
		SourceEventID: row.SourceEventID,
		TargetEventID: row.TargetEventID,
		StartsAt:      timeFromTimestamptz(row.StartsAt),
		EndsAt:        timeFromTimestamptz(row.EndsAt),
		ContentHash:   row.ContentHash,
		Details:       details,
		CreatedAt:     timeFromTimestamptz(row.CreatedAt),
		UpdatedAt:     timeFromTimestamptz(row.UpdatedAt),
	}, nil
}
