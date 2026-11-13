// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// errStoreUnavailable is what a test injects to prove a failure of the
// store reaches the caller as itself rather than as a login outcome.
var errStoreUnavailable = errors.New("the store is unavailable")

// createUser puts an ordinary account in the store, for a test that
// needs somebody to already be there.
func createUser(ctx context.Context, t *testing.T, store *storagetest.Store, email string) storage.User {
	t.Helper()

	user, err := store.Users().Create(ctx, storage.User{
		Email:  email,
		Status: storage.UserStatusActive,
	})
	if err != nil {
		t.Fatalf("create the user %s: %v", email, err)
	}

	return user
}

// disableAccount marks an account disabled, a state no ordinary call
// produces and that both login paths have to refuse.
func disableAccount(t *testing.T, store *storagetest.Store, id uuid.UUID) {
	t.Helper()

	user, ok := store.User(id)
	if !ok {
		t.Fatalf("no account %s to disable", id)
	}

	user.Status = storage.UserStatusDisabled
	store.SetUser(user)
}
