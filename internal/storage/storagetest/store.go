// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package storagetest is an in-memory implementation of the repository
// interfaces internal/storage defines, for a test of a package above the
// storage layer that needs somewhere for accounts to live but no
// database. internal/storage/postgres stays the only real
// implementation; nothing outside a test imports this.
//
// It is a fake, not a mock, per go-tests: a test asserts on the state it
// leaves behind rather than on the calls it made. The one exception is
// ConnectionReads, which exists so internal/auth's test can prove that
// resolving a sign-in never consulted a calendar connection at all; a
// fake with no connections in it could not tell "never looked" apart
// from "nothing to find".
//
// The scoping it imitates is deliberately strict: a repository method
// that the real implementation requires a WithUser transaction for fails
// here with storage.ErrNoUserScope outside one, and a row belonging to
// another user is invisible inside one, which is what Postgres row level
// security does for the real thing.
package storagetest

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// fixedTime is what every row here is created and updated at. A test
// that cares about time passes its own clock to the package under test;
// nothing in this package reads the real one.
var fixedTime = time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

// Store is the in-memory store. Build one with NewStore; the exported
// fields are the hooks a test reaches for.
type Store struct {
	mu sync.Mutex

	users       map[uuid.UUID]storage.User
	identities  map[uuid.UUID]storage.Identity
	connections map[uuid.UUID]storage.Connection

	// ConnectionReads counts every read through the connection
	// repository. See the package doc.
	ConnectionReads int

	// FailUserCreate, when set, is what Users().Create returns instead
	// of creating anything, so a test can exercise the path where the
	// database itself is unavailable.
	FailUserCreate error

	// hideIdentities makes FindByProviderSubject answer "not found" for
	// an identity that is really there, while Create still collides with
	// it. See HideIdentities.
	hideIdentities bool

	scope *uuid.UUID
}

// HideIdentities makes the identity lookup answer "not found" while the
// rows are still there to collide with on a write.
//
// It reproduces, deterministically, what two sign-ins racing for the
// same provider subject do to each other: both look, neither finds, both
// create an account, and the second loses the uniqueness check on the
// identity itself. A test cannot schedule that race reliably, and the
// path it leads to, the losing request undoing the account it just
// created, is the one that decides whether an address stays claimed for
// good.
func (s *Store) HideIdentities(hide bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hideIdentities = hide
}

// NewStore builds an empty store.
func NewStore() *Store {
	return &Store{
		users:       map[uuid.UUID]storage.User{},
		identities:  map[uuid.UUID]storage.Identity{},
		connections: map[uuid.UUID]storage.Connection{},
	}
}

// WithUser implements storage.UserScope.
func (s *Store) WithUser(ctx context.Context, userID uuid.UUID, fn func(context.Context) error) error {
	s.mu.Lock()
	previous := s.scope
	s.scope = &userID
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.scope = previous
		s.mu.Unlock()
	}()

	return fn(ctx)
}

// Users returns the user repository.
func (s *Store) Users() storage.UserRepository { return &users{store: s} }

// Identities returns the identity repository.
func (s *Store) Identities() storage.IdentityRepository { return &identities{store: s} }

// Connections returns the connection repository.
func (s *Store) Connections() storage.ConnectionRepository { return &connections{store: s} }

// User returns the stored row for id, for a test asserting on what a
// call left behind.
func (s *Store) User(id uuid.UUID) (storage.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id]

	return user, ok
}

// SetUser replaces a stored row, for a test that needs an account in a
// state no ordinary call produces, such as disabled.
func (s *Store) SetUser(user storage.User) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[user.ID] = user
}

// CountUsers reports how many accounts are stored.
func (s *Store) CountUsers() int { return s.count(func() int { return len(s.users) }) }

// CountIdentities reports how many sign-in identities are stored.
func (s *Store) CountIdentities() int { return s.count(func() int { return len(s.identities) }) }

// CountConnections reports how many calendar connections are stored.
func (s *Store) CountConnections() int { return s.count(func() int { return len(s.connections) }) }

func (s *Store) count(of func() int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return of()
}

// scoped returns the user the current transaction is scoped to, or
// storage.ErrNoUserScope. The caller already holds the lock.
func (s *Store) scoped() (uuid.UUID, error) {
	if s.scope == nil {
		return uuid.Nil, storage.ErrNoUserScope
	}

	return *s.scope, nil
}

type users struct{ store *Store }

// Create implements storage.UserRepository. Like the real one it is not
// scoped: its caller has no user id to scope by yet.
func (r *users) Create(_ context.Context, user storage.User) (storage.User, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if r.store.FailUserCreate != nil {
		return storage.User{}, r.store.FailUserCreate
	}

	for _, existing := range r.store.users {
		if strings.EqualFold(existing.Email, user.Email) {
			return storage.User{}, storage.ErrConflict
		}
	}

	user.ID = uuid.New()
	user.CreatedAt = fixedTime
	user.UpdatedAt = fixedTime
	r.store.users[user.ID] = user

	return user, nil
}

// FindByID implements storage.UserRepository.
func (r *users) FindByID(_ context.Context, id uuid.UUID) (storage.User, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.User{}, err
	}

	user, ok := r.store.users[id]
	if !ok || user.ID != scope {
		return storage.User{}, storage.ErrNotFound
	}

	return user, nil
}

// FindByEmail implements storage.UserRepository. It is not scoped,
// matching the real repository: the email lookup is what runs before any
// user id is known.
func (r *users) FindByEmail(_ context.Context, email string) (storage.User, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for _, user := range r.store.users {
		if strings.EqualFold(user.Email, email) {
			return user, nil
		}
	}

	return storage.User{}, storage.ErrNotFound
}

// Update implements storage.UserRepository.
func (r *users) Update(_ context.Context, user storage.User) (storage.User, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.User{}, err
	}

	if _, ok := r.store.users[user.ID]; !ok || user.ID != scope {
		return storage.User{}, storage.ErrNotFound
	}

	user.UpdatedAt = fixedTime
	r.store.users[user.ID] = user

	return user, nil
}

// AdvanceTOTPCounter implements storage.UserRepository. The comparison
// and the write happen under the store's own lock, which is what the
// real implementation gets from doing both in one SQL statement: two
// logins racing with the same code cannot both advance the counter here
// either, so a test can exercise the replay refusal without a database.
func (r *users) AdvanceTOTPCounter(_ context.Context, id uuid.UUID, counter int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return err
	}

	user, ok := r.store.users[id]
	if !ok || user.ID != scope {
		return storage.ErrNotFound
	}

	if user.TOTPLastCounter != nil && counter <= *user.TOTPLastCounter {
		return storage.ErrNotFound
	}

	advanced := counter
	user.TOTPLastCounter = &advanced
	user.UpdatedAt = fixedTime
	r.store.users[id] = user

	return nil
}

// DeleteAccount implements storage.UserRepository. It removes the user
// and everything it owns, in the same order the real implementation's
// queries/account_deletion.sql does.
func (r *users) DeleteAccount(_ context.Context, id uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if _, ok := r.store.users[id]; !ok {
		return storage.ErrNotFound
	}

	for connectionID, connection := range r.store.connections {
		if connection.UserID == id {
			delete(r.store.connections, connectionID)
		}
	}

	for identityID, identity := range r.store.identities {
		if identity.UserID == id {
			delete(r.store.identities, identityID)
		}
	}

	delete(r.store.users, id)

	return nil
}

type identities struct{ store *Store }

// Create implements storage.IdentityRepository.
func (r *identities) Create(_ context.Context, identity storage.Identity) (storage.Identity, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Identity{}, err
	}

	if identity.UserID != scope {
		return storage.Identity{}, storage.ErrNotFound
	}

	for _, existing := range r.store.identities {
		if existing.Provider == identity.Provider && existing.Subject == identity.Subject {
			return storage.Identity{}, storage.ErrConflict
		}
	}

	identity.ID = uuid.New()
	identity.CreatedAt = fixedTime
	r.store.identities[identity.ID] = identity

	return identity, nil
}

// FindByID implements storage.IdentityRepository.
func (r *identities) FindByID(_ context.Context, id uuid.UUID) (storage.Identity, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Identity{}, err
	}

	identity, ok := r.store.identities[id]
	if !ok || identity.UserID != scope {
		return storage.Identity{}, storage.ErrNotFound
	}

	return identity, nil
}

// FindByProviderSubject implements storage.IdentityRepository. It is not
// scoped, matching the real repository, and reads identities and nothing
// else, exactly like the SECURITY DEFINER function behind the real one.
func (r *identities) FindByProviderSubject(
	_ context.Context,
	provider storage.IdentityProvider,
	subject string,
) (storage.Identity, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if r.store.hideIdentities {
		return storage.Identity{}, storage.ErrNotFound
	}

	for _, identity := range r.store.identities {
		if identity.Provider == provider && identity.Subject == subject {
			return identity, nil
		}
	}

	return storage.Identity{}, storage.ErrNotFound
}

// List implements storage.IdentityRepository.
func (r *identities) List(_ context.Context) ([]storage.Identity, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	var found []storage.Identity

	for _, identity := range r.store.identities {
		if identity.UserID == scope {
			found = append(found, identity)
		}
	}

	return found, nil
}

// Delete implements storage.IdentityRepository.
func (r *identities) Delete(_ context.Context, id uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return err
	}

	identity, ok := r.store.identities[id]
	if !ok || identity.UserID != scope {
		return storage.ErrNotFound
	}

	delete(r.store.identities, id)

	return nil
}

type connections struct{ store *Store }

// Create implements storage.ConnectionRepository.
func (r *connections) Create(_ context.Context, connection storage.Connection) (storage.Connection, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Connection{}, err
	}

	if connection.UserID != scope {
		return storage.Connection{}, storage.ErrNotFound
	}

	connection.ID = uuid.New()
	connection.CreatedAt = fixedTime
	connection.UpdatedAt = fixedTime
	r.store.connections[connection.ID] = connection

	return connection, nil
}

// FindByID implements storage.ConnectionRepository and counts the read;
// see the package doc.
func (r *connections) FindByID(_ context.Context, id uuid.UUID) (storage.Connection, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	r.store.ConnectionReads++

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Connection{}, err
	}

	connection, ok := r.store.connections[id]
	if !ok || connection.UserID != scope {
		return storage.Connection{}, storage.ErrNotFound
	}

	return connection, nil
}

// List implements storage.ConnectionRepository and counts the read; see
// the package doc.
func (r *connections) List(_ context.Context) ([]storage.Connection, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	r.store.ConnectionReads++

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	var found []storage.Connection

	for _, connection := range r.store.connections {
		if connection.UserID == scope {
			found = append(found, connection)
		}
	}

	return found, nil
}

// UpdateCredentials implements storage.ConnectionRepository.
func (r *connections) UpdateCredentials(
	_ context.Context,
	id uuid.UUID,
	credentials []byte,
) (storage.Connection, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return storage.Connection{}, err
	}

	connection, ok := r.store.connections[id]
	if !ok || connection.UserID != scope {
		return storage.Connection{}, storage.ErrNotFound
	}

	connection.Credentials = credentials
	connection.UpdatedAt = fixedTime
	r.store.connections[id] = connection

	return connection, nil
}

// Delete implements storage.ConnectionRepository.
func (r *connections) Delete(_ context.Context, id uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return err
	}

	connection, ok := r.store.connections[id]
	if !ok || connection.UserID != scope {
		return storage.ErrNotFound
	}

	delete(r.store.connections, id)

	return nil
}
