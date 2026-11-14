// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package storage defines the domain types and repository interfaces that
// later business logic (internal/auth, internal/provider and internal/sync)
// calls to persist and query users, identities, connections, calendars,
// edges, grants and the event mapping. internal/storage/postgres is the
// only implementation; nothing here leaks a pgx or sqlc type.
package storage

import (
	"time"

	"github.com/google/uuid"
)

// UserStatus is the lifecycle state of a user account.
type UserStatus string

const (
	// UserStatusActive can sign in and use the application.
	UserStatusActive UserStatus = "active"
	// UserStatusDisabled had its access revoked.
	UserStatusDisabled UserStatus = "disabled"
)

// User is the root of per-user isolation: every other user-owned row
// carries a UserID referencing one. User itself carries no user_id
// column, because a row here IS a user, not something owned by one, so
// its row level security policy scopes by the row's own id instead; see
// db/migrations for the full reasoning.
// PasswordHash and TOTPSecret are nil until internal/auth, a later step,
// computes and sets them; this package never hashes a password or
// generates a TOTP secret, it only stores and returns whatever
// internal/auth already computed.
type User struct {
	ID              uuid.UUID
	Email           string
	PasswordHash    *string
	TOTPSecret      *string
	TOTPLastCounter *int64
	Status          UserStatus
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// IdentityProvider is a provider a user can sign in with. It is
// deliberately smaller than ConnectionProvider: Apple is offered as a
// sign-in, which grants identity and nothing else, while an iCloud
// calendar is connected separately over CalDAV and so never appears here.
type IdentityProvider string

// The identity providers a user can sign in with.
const (
	IdentityProviderGoogle    IdentityProvider = "google"
	IdentityProviderMicrosoft IdentityProvider = "microsoft"
	IdentityProviderApple     IdentityProvider = "apple"
)

// Identity is a provider identity used to sign in, scoped to one user.
// It is a separate type, backed by a separate table, from Connection: a
// provider identity used to sign in is never resolved through a
// connection someone else connected as a calendar, and a connection is
// never resolved through an identity; see db/migrations's own reasoning.
type Identity struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Provider IdentityProvider
	// Subject is the identity provider's stable subject claim, never the
	// email or username, both of which can be reassigned to somebody
	// else.
	Subject   string
	CreatedAt time.Time
}

// ConnectionProvider is a provider a calendar connection can use.
// "caldav" covers every CalDAV server a profile exists for, iCloud
// included; internal/provider, a later step, is what interprets the
// profile.
type ConnectionProvider string

// The providers a calendar connection can use.
const (
	ConnectionProviderGoogle    ConnectionProvider = "google"
	ConnectionProviderMicrosoft ConnectionProvider = "microsoft"
	ConnectionProviderCalDAV    ConnectionProvider = "caldav"
)

// Connection is a provider account connected as a calendar, scoped to one
// user. Credentials is the plain, serialized credential (an OAuth
// refresh token, or an Apple ID and app-specific password), typed and
// serialized by internal/provider, a later step; this package only ever
// sees opaque bytes. internal/storage/postgres encrypts Credentials
// before it reaches the database and decrypts it on the way back out, so
// nothing above this package, and nothing in the database itself, ever
// holds or stores it in clear.
type Connection struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Provider    ConnectionProvider
	DisplayName string
	Credentials []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Calendar is one calendar inside one connection: either an existing
// calendar the user selected, or one calbridge created and owns
// (Managed = true).
type Calendar struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ConnectionID uuid.UUID
	ExternalID   string
	Name         string
	Managed      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SharedCalendar is a calendar somebody else owns and shared with this
// user, read back together with the grant that shares it. The two travel
// as one value because a shared calendar is only ever usable up to the
// level its grant allows, and holding the calendar without the bound is
// how a caller ends up offering more detail than the owner permitted.
//
// UserID on the embedded Calendar is the owner's, not the reader's: this
// is the one place in this package where a Calendar comes back under a
// user id other than the scope it was read in.
type SharedCalendar struct {
	Calendar

	// GrantID is the calendar_grants row that shares it, so revoking the
	// share needs no second lookup.
	GrantID uuid.UUID
	// MaxDetailLevel is the most an edge over this calendar may copy.
	// internal/sync is what enforces it; nothing here interprets it.
	MaxDetailLevel DetailLevel
}

// GrantCounterparty is the person on the other side of a grant this user
// is party to: the grantee of a grant they made, or the owner of one
// made to them. It carries the address and nothing else, because the
// address is the whole of what a sharing screen needs and the rest of a
// users row is not this user's to read; see db/migrations's own
// grant_counterparties.
type GrantCounterparty struct {
	UserID uuid.UUID
	Email  string
}

// DetailLevel is a sync edge's or a grant's detail level preset; see
// docs/concepts/calbridge.md. A per-field override for a case a preset
// misses is internal/sync's concern, a later step.
type DetailLevel string

// The detail level presets docs/concepts/calbridge.md defines.
const (
	DetailLevelFull      DetailLevel = "full"
	DetailLevelTitleTime DetailLevel = "title_time"
	DetailLevelBusyBlock DetailLevel = "busy_block"
)

// Edge is a sync edge: copies events from one source calendar into one
// target calendar in one direction, at a fixed DetailLevel. UserID is the
// edge's owner, the person who built it, which is not always the owner
// of both calendars: see db/migrations's own reasoning for the
// cross-user, grant-bounded case.
type Edge struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	SourceCalendarID uuid.UUID
	TargetCalendarID uuid.UUID
	DetailLevel      DetailLevel
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CalendarGrant is its own record: a user (OwnerUserID) grants another
// user (GranteeUserID) access to one of their calendars at a maximum
// detail level, and the grantee's edges are bounded by it. Checking that
// an edge actually stays within its grant's bound is internal/sync's job,
// a later step.
type CalendarGrant struct {
	ID             uuid.UUID
	OwnerUserID    uuid.UUID
	GranteeUserID  uuid.UUID
	CalendarID     uuid.UUID
	MaxDetailLevel DetailLevel
	CreatedAt      time.Time
}

// EventMapping is the sync engine's mapping table: edge id, source event
// id, target event id, start, end and a content hash, so the database
// alone does not read as a calendar. Details is a copied title or body
// in clear, nil when the edge's detail level copies neither (for example
// DetailLevelBusyBlock); internal/storage/postgres encrypts it before it
// reaches the database and decrypts it on the way back out, the same
// transparent handling Connection.Credentials gets. internal/sync, a
// later step, is the only package meant to know what ContentHash and
// Details are used for.
type EventMapping struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	EdgeID        uuid.UUID
	SourceEventID string
	TargetEventID string
	StartsAt      time.Time
	EndsAt        time.Time
	ContentHash   string
	Details       []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
