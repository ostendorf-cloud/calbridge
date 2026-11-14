// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// ErrAccountNotFound is returned by DeleteAccount for a user id with no
// row behind it, which in practice is a session outliving the account it
// was started for.
var ErrAccountNotFound = errors.New("auth: no such account")

// DeleteAccount removes an account and everything it owns: its sign-in
// identities, its calendar connections, its calendars, its edges, the
// grants on either side of it, and every row its edges wrote into the
// event mapping. It is one transaction, so a failure anywhere leaves the
// account whole rather than half deleted.
//
// What it deliberately does not do is reach into a provider. An edge
// that wrote events into a calendar at Google or Microsoft leaves those
// events behind, because deleting them needs a connector to call and
// this package has none; docs/concepts/calbridge.md's own "deleting an
// edge removes every event it wrote" belongs to internal/sync, which
// owns the connectors and the mapping table's meaning. The credential
// those events were written with is gone with the connection either way,
// so nothing continues to write after this returns.
//
// Ending the person's sessions is the caller's job and not this one's:
// session.Manager.EndAll keys on the subject Identity.Session puts there,
// and internal/httpapi holds the manager.
func DeleteAccount(ctx context.Context, store Store, userID uuid.UUID) error {
	if err := store.Users().DeleteAccount(ctx, userID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrAccountNotFound
		}

		return fmt.Errorf("auth: delete account: %w", err)
	}

	return nil
}
