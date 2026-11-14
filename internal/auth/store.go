// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import "github.com/ostendorf-cloud/calbridge/internal/storage"

// Store is what this package needs from a storage backend: the
// user-scoping mechanism, and the user and identity repositories. It is
// defined here, at the consumer, per go-conventions, rather than in
// internal/storage: internal/storage/postgres.Store already satisfies
// it, with no adapter, because its method set is a superset of this one.
//
// What is deliberately absent is the whole point. There is no
// Connections method here, so no code in this package can reach a
// provider account somebody connected as a calendar, which is what makes
// the linking rule in the package doc structural rather than a check
// somebody could forget to write. Adding one here would remove that
// guarantee, whatever the intended use.
//
// Nothing in this package imports internal/storage/postgres or pgx
// directly; a test substitutes a hand written fake.
type Store interface {
	storage.UserScope

	Users() storage.UserRepository
	Identities() storage.IdentityRepository
}
