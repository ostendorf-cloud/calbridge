// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"

	"github.com/google/uuid"
)

// UserScope is the one mechanism through which every user-scoped
// repository method in this package is allowed to run. WithUser opens a
// transaction, scopes it to userID (set_config('app.user_id', ...),
// transaction-local) and runs fn with a context carrying that
// transaction; it commits when fn returns nil and rolls back otherwise.
//
// A user-scoped repository method called with a context WithUser did not
// produce fails with ErrNoUserScope instead of silently running
// unscoped: row level security would already turn an unscoped query into
// zero rows read and zero rows written, but failing in Go as well means
// that mistake surfaces immediately, as a bug, rather than as a
// confusing empty result.
type UserScope interface {
	WithUser(ctx context.Context, userID uuid.UUID, fn func(ctx context.Context) error) error
}

// UserRepository persists and queries users themselves. Its methods are
// deliberately not user-scoped: looking a user up by email, to check its
// password, is exactly the step that has to run before any user scope
// exists.
type UserRepository interface {
	Create(ctx context.Context, user User) (User, error)
	FindByID(ctx context.Context, id uuid.UUID) (User, error)
	FindByEmail(ctx context.Context, email string) (User, error)
	Update(ctx context.Context, user User) (User, error)
}

// IdentityRepository persists and queries provider identities used to
// sign in. Every method must run inside a context UserScope.WithUser
// produced; see ErrNoUserScope. There is deliberately no method that
// resolves a (provider, subject) pair to a user across users: that
// lookup, needed before any user scope exists, is internal/auth's
// problem to solve in a later step, and is not this package's to solve
// by exempting this table from row level security the way User is
// exempted; see db/migrations's own reasoning on the identities table.
type IdentityRepository interface {
	Create(ctx context.Context, identity Identity) (Identity, error)
	FindByID(ctx context.Context, id uuid.UUID) (Identity, error)
	List(ctx context.Context) ([]Identity, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// ConnectionRepository persists and queries provider accounts connected
// as calendars. Every method must run inside a context
// UserScope.WithUser produced; see ErrNoUserScope. Create and
// UpdateCredentials take, and every read returns, Connection.Credentials
// in clear: internal/storage/postgres encrypts and decrypts it at the
// database boundary itself, so no caller has to remember to.
type ConnectionRepository interface {
	Create(ctx context.Context, connection Connection) (Connection, error)
	FindByID(ctx context.Context, id uuid.UUID) (Connection, error)
	List(ctx context.Context) ([]Connection, error)
	UpdateCredentials(ctx context.Context, id uuid.UUID, credentials []byte) (Connection, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// CalendarRepository persists and queries calendars. Every method must
// run inside a context UserScope.WithUser produced; see ErrNoUserScope.
type CalendarRepository interface {
	Create(ctx context.Context, calendar Calendar) (Calendar, error)
	FindByID(ctx context.Context, id uuid.UUID) (Calendar, error)
	List(ctx context.Context) ([]Calendar, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// EdgeRepository persists and queries sync edges. Every method must run
// inside a context UserScope.WithUser produced; see ErrNoUserScope.
type EdgeRepository interface {
	Create(ctx context.Context, edge Edge) (Edge, error)
	FindByID(ctx context.Context, id uuid.UUID) (Edge, error)
	List(ctx context.Context) ([]Edge, error)
	UpdateDetailLevel(ctx context.Context, id uuid.UUID, detailLevel DetailLevel) (Edge, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// CalendarGrantRepository persists and queries calendar grants. Every
// method must run inside a context UserScope.WithUser produced; see
// ErrNoUserScope. List and Delete are visible to either side of a grant,
// the owner and the grantee, matching db/migrations's own row level
// security policy: a grantee builds edges against what was shared with
// them and may delete a grant made to them the same way, but the
// database itself (see that migration's WITH CHECK clause) refuses an
// Update from any scope other than the owner's.
type CalendarGrantRepository interface {
	Create(ctx context.Context, grant CalendarGrant) (CalendarGrant, error)
	FindByID(ctx context.Context, id uuid.UUID) (CalendarGrant, error)
	List(ctx context.Context) ([]CalendarGrant, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// EventMappingRepository persists and queries the sync engine's mapping
// table. Every method must run inside a context UserScope.WithUser
// produced; see ErrNoUserScope. EventMapping.Details on Create, Update
// and every read is in clear: internal/storage/postgres encrypts and
// decrypts it at the database boundary itself, so no caller has to
// remember to.
type EventMappingRepository interface {
	Create(ctx context.Context, mapping EventMapping) (EventMapping, error)
	FindByID(ctx context.Context, id uuid.UUID) (EventMapping, error)
	FindByEdgeAndSourceEventID(ctx context.Context, edgeID uuid.UUID, sourceEventID string) (EventMapping, error)
	Update(ctx context.Context, mapping EventMapping) (EventMapping, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
