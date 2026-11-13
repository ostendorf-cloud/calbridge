// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the only implementation of the repository
// interfaces internal/storage defines. Nothing outside this package
// imports pgx or the generated sqlc code directly.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// Store wires a connection pool, an encryptor and the user-scoping
// mechanism into every repository. Store satisfies storage.UserScope
// itself; its named methods return the repository interfaces.
type Store struct {
	pool      *pgxpool.Pool
	encryptor *encryptor
}

// NewPool opens a pgxpool.Pool against connString and verifies it can
// reach the database before returning. ctx bounds only this initial
// connection attempt, not the pool's later lifetime.
//
// connString is expected to already be a fully formed connection string.
// cmd/calbridge's production path does not call this: see
// NewPoolWithCredentials for why a single string embedding the real
// database password is never constructed there. NewPool remains for
// callers, today only tests, that already have a complete, non-secret
// connection string (a fixed, documented-fake test password) to pass.
func NewPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}

// NewPoolWithCredentials opens a pool the same way NewPool does, but
// never constructs a single string embedding the password: user and
// password are set directly on the parsed pgxpool.Config's ConnConfig, so
// no error path that might echo the connection string (pgx's own
// redaction is documented as best-effort, not something to rely on) can
// ever carry the real secret. This is the production call path
// (internal/config's real secret); NewPool's existing test callers use a
// fixed, documented-fake test password and don't need this.
func NewPoolWithCredentials(ctx context.Context, databaseURL, user, password string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}

	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}

// NewStore wires an already-open pool and an encryption key into a
// Store. Most callers reach this through NewPool or
// NewPoolWithCredentials; a test that shares one pool across several
// stores, or across a restricted and an administrative role, calls it
// directly. key is the raw encryption key connections.credentials and
// event_mapping.encrypted_details are encrypted and decrypted with; see
// newEncryptor for its length requirement.
func NewStore(pool *pgxpool.Pool, key []byte) (*Store, error) {
	enc, err := newEncryptor(key)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	return &Store{pool: pool, encryptor: enc}, nil
}

// Close releases the underlying pool.
func (s *Store) Close() {
	s.pool.Close()
}

// Users returns the user repository. Its methods are not user-scoped;
// see storage.UserRepository.
func (s *Store) Users() storage.UserRepository {
	return &userRepository{pool: s.pool}
}

// Identities returns the identity repository. Every method on it
// requires a context WithUser produced; see storage.IdentityRepository.
func (s *Store) Identities() storage.IdentityRepository {
	return &identityRepository{}
}

// Connections returns the connection repository. Every method on it
// requires a context WithUser produced; see storage.ConnectionRepository.
func (s *Store) Connections() storage.ConnectionRepository {
	return &connectionRepository{encryptor: s.encryptor}
}

// Calendars returns the calendar repository. Every method on it requires
// a context WithUser produced; see storage.CalendarRepository.
func (s *Store) Calendars() storage.CalendarRepository {
	return &calendarRepository{}
}

// Edges returns the edge repository. Every method on it requires a
// context WithUser produced; see storage.EdgeRepository.
func (s *Store) Edges() storage.EdgeRepository {
	return &edgeRepository{}
}

// CalendarGrants returns the calendar grant repository. Every method on
// it requires a context WithUser produced; see
// storage.CalendarGrantRepository.
func (s *Store) CalendarGrants() storage.CalendarGrantRepository {
	return &calendarGrantRepository{}
}

// EventMappings returns the event mapping repository. Every method on it
// requires a context WithUser produced; see
// storage.EventMappingRepository.
func (s *Store) EventMappings() storage.EventMappingRepository {
	return &eventMappingRepository{encryptor: s.encryptor}
}
