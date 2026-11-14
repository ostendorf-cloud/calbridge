// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import (
	"github.com/google/uuid"

	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// Identity is what every login path in this package produces, and the
// only shape any of them returns. See the package doc for why there is
// exactly one.
type Identity struct {
	// UserID is the users row behind this login, whichever path produced
	// it. It is what [Identity.Session] uses as the session's Subject,
	// never a provider's subject claim, so session.Manager.EndAll revokes
	// every session of an account regardless of how each one started.
	UserID uuid.UUID

	// Email is read off the users row, as a display attribute. It is
	// never what a login was matched on: a local login matches on the
	// email the account was created under and then on its password and
	// TOTP code, and a provider login matches on the provider's subject
	// claim alone.
	Email string
}

// identityFromUser builds the Identity a user row backs. Both paths end
// here, which is what keeps the two from drifting apart.
func identityFromUser(user storage.User) Identity {
	return Identity{UserID: user.ID, Email: user.Email}
}

// Session builds the session.Session internal/httpapi passes to
// session.Manager.Start, the one call both login paths end at.
//
// The returned value still needs session.Manager.Start to fill in ID,
// IssuedAt, LastSeen and ExpiresAt: those are the manager's to set, not
// this package's, exactly as session.Session itself documents.
func (id Identity) Session() session.Session {
	return session.Session{
		Subject:  id.UserID.String(),
		Username: id.Email,
		Email:    id.Email,
	}
}
