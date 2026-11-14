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

// UserRepository persists and queries users themselves. FindByEmail is
// deliberately not user-scoped: looking a user up by email, to check its
// password, is exactly the step that has to run before any user scope
// exists, and it reaches the row through a SECURITY DEFINER function,
// never by opening a row level security policy to every row (see
// db/migrations). FindByID and Update must run inside a context
// UserScope.WithUser produced, like every other repository; see
// ErrNoUserScope. Create and DeleteAccount take a plain context instead,
// because neither caller has a scope to work inside: Create generates the
// new user's id and scopes its own transaction to it, so signup is
// covered by the same policy as every other command rather than exempt
// from it, and DeleteAccount scopes its own transaction to the user it
// removes. Any ID set on the passed User is ignored; the returned User
// carries the id the row was actually created with.
//
// AdvanceTOTPCounter writes the RFC 6238 counter a login's code matched,
// and only that column, so a login never writes back a password hash, a
// TOTP secret or a status read before the password verify. It advances
// the counter only when the stored one is lower or absent, and returns
// ErrNotFound when no row changed, which is how a replayed code is
// refused: the comparison and the write are one statement, so two logins
// racing with the same code cannot both pass it. A caller treats
// ErrNotFound as a refused login, never as a missing account.
//
// DeleteAccount removes the user and everything it owns: its identities,
// its connections, its calendars, its edges, the grants on either side
// of it, and every row its edges wrote into the event mapping. There is
// no ON DELETE CASCADE anywhere in this schema, so the order those rows
// go in is part of the contract; see internal/storage/postgres's
// queries/account_deletion.sql.
type UserRepository interface {
	Create(ctx context.Context, user User) (User, error)
	FindByID(ctx context.Context, id uuid.UUID) (User, error)
	FindByEmail(ctx context.Context, email string) (User, error)
	Update(ctx context.Context, user User) (User, error)
	AdvanceTOTPCounter(ctx context.Context, id uuid.UUID, counter int64) error
	DeleteAccount(ctx context.Context, id uuid.UUID) error
}

// IdentityRepository persists and queries provider identities used to
// sign in. Every method except FindByProviderSubject must run inside a
// context UserScope.WithUser produced; see ErrNoUserScope.
//
// FindByProviderSubject is the one exception, and it is the same
// exception UserRepository.FindByEmail already is: resolving a provider
// sign-in to a user is the step that has to run before any user scope
// exists. It reaches the row through a SECURITY DEFINER function that
// reads identities and nothing else, never by opening a row level
// security policy to every row.
//
// It matches on the identity provider's stable subject claim and on
// nothing else. In particular there is no method here, and there may
// never be one, that resolves a sign-in through a ConnectionRepository
// row: a provider account another user connected as a calendar must
// never be matched to a sign-in identity, or controlling a mailbox whose
// calendar someone connected becomes a path into that person's calbridge
// account. The collision creates a separate account instead; see
// db/migrations's own reasoning on the identities table.
type IdentityRepository interface {
	Create(ctx context.Context, identity Identity) (Identity, error)
	FindByID(ctx context.Context, id uuid.UUID) (Identity, error)
	FindByProviderSubject(ctx context.Context, provider IdentityProvider, subject string) (Identity, error)
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

// CalendarRepository persists and queries the calendars one user owns.
// Every method must run inside a context UserScope.WithUser produced;
// see ErrNoUserScope. A calendar somebody else owns and shared is
// deliberately out of reach here, FindByID included, so no caller can
// hold a shared calendar without the grant that bounds it; it comes
// back through SharingRepository instead.
type CalendarRepository interface {
	Create(ctx context.Context, calendar Calendar) (Calendar, error)
	FindByID(ctx context.Context, id uuid.UUID) (Calendar, error)
	List(ctx context.Context) ([]Calendar, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// SharingRepository answers the three questions that cross the boundary
// between one user and another, and it is deliberately its own
// interface rather than three methods spread over the repositories
// above: everything reachable through it is reachable only because a
// grant, or the person creating one, named somebody explicitly, and
// keeping that in one place is what makes the whole of that surface
// readable at once.
//
// ListCalendarsSharedWithMe and ListGrantCounterparties must run inside
// a context UserScope.WithUser produced; see ErrNoUserScope. Each
// returns a shared calendar with the grant that bounds it, and the
// address of each person on the other side of a grant this user is
// party to, respectively. Neither takes a parameter naming whose rows
// to read: the scope is the whole of the question.
//
// FindUserIDByEmail is the one method here that is not user-scoped, for
// the same reason UserRepository.FindByEmail is not: it has to answer
// for an address that is not the caller's own, so no scope would admit
// the row. It answers which user id an address belongs to and nothing
// else, so the caller that creates a grant never holds a password hash
// or a TOTP secret it has no use for. ErrNotFound means no account has
// that address; see internal/storage/postgres's queries/users.sql for
// why that is said plainly rather than hidden.
type SharingRepository interface {
	FindUserIDByEmail(ctx context.Context, email string) (uuid.UUID, error)
	ListCalendarsSharedWithMe(ctx context.Context) ([]SharedCalendar, error)
	ListGrantCounterparties(ctx context.Context) ([]GrantCounterparty, error)
}

// EdgeRepository persists and queries sync edges. Every method must run
// inside a context UserScope.WithUser produced; see ErrNoUserScope.
// ListByCalendar answers the question internal/sync asks when a grant is
// revoked: which of this user's edges touch that calendar, on either
// side, and therefore have to stop and give back what they wrote.
type EdgeRepository interface {
	Create(ctx context.Context, edge Edge) (Edge, error)
	FindByID(ctx context.Context, id uuid.UUID) (Edge, error)
	List(ctx context.Context) ([]Edge, error)
	ListByCalendar(ctx context.Context, calendarID uuid.UUID) ([]Edge, error)
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
//
// A grant row names its two users by id and nothing more. Who those ids
// are is SharingRepository's question, not this one's.
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
//
// ListByEdge returns every copy one edge made, which is what a full
// reconcile compares the source against and what a teardown deletes.
// DeleteByEdge removes those rows in one statement and reports how many
// it removed, so a teardown forgets an edge's copies without reading
// them back one at a time. It runs in a transaction of its own, before
// the edge row is deleted in another: the events themselves are removed
// at the provider in between, and a database transaction is not held
// open across a network call.
type EventMappingRepository interface {
	Create(ctx context.Context, mapping EventMapping) (EventMapping, error)
	FindByID(ctx context.Context, id uuid.UUID) (EventMapping, error)
	FindByEdgeAndSourceEventID(ctx context.Context, edgeID uuid.UUID, sourceEventID string) (EventMapping, error)
	ListByEdge(ctx context.Context, edgeID uuid.UUID) ([]EventMapping, error)
	Update(ctx context.Context, mapping EventMapping) (EventMapping, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteByEdge(ctx context.Context, edgeID uuid.UUID) (int64, error)
}
