// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/govue/oidc"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// errStoreUnavailable is what a test injects to prove a failure of the
// store reaches the caller as itself rather than as a login outcome.
var errStoreUnavailable = errors.New("the store is unavailable")

// A note on t.Parallel in this package: a test that performs a real
// Argon2id derivation does not call it.
//
// auth.MaxConcurrentPasswordHashes is process-wide, and a derivation is
// CPU bound rather than waiting on anything, so running such tests in
// parallel does not make them finish sooner. It only queues them behind
// each other on the same two slots, and with the race detector making
// each derivation several times slower, a long enough queue starts
// shedding with ErrHashingBusy and the suite fails for a reason that has
// nothing to do with what it is testing.
//
// The tests that exercise the cap itself replace the derivation with a
// stand-in (see auth.SetDeriveKey) and must not be parallel for a
// second reason: they write package state.

// signedInAs builds the oidc.Identity govue hands over once it has
// verified an ID token, including the token itself: internal/auth reads
// the email_verified claim back out of it, so a test that left IDToken
// empty would be testing the unverified path whatever Email said.
//
// The token is shaped like a JWT and is not signed. Nothing in this
// package checks a signature: by the time SignIn runs, govue has already
// verified the real token's signature, issuer, audience, expiry and
// nonce, and this package only reads one more claim out of what it was
// handed.
func signedInAs(t *testing.T, subject, email string, emailVerified bool) oidc.Identity {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"sub":            subject,
		"email":          email,
		"email_verified": emailVerified,
	})
	if err != nil {
		t.Fatalf("encode the id token payload: %v", err)
	}

	return oidc.Identity{
		Subject: subject,
		Email:   email,
		IDToken: "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature",
	}
}

// signedInWithoutAnEmailClaim is what a provider that was never asked
// for the email scope hands over: a subject and nothing else. Apple is
// the one calbridge configures that way.
func signedInWithoutAnEmailClaim(t *testing.T, subject string) oidc.Identity {
	t.Helper()

	payload, err := json.Marshal(map[string]any{"sub": subject})
	if err != nil {
		t.Fatalf("encode the id token payload: %v", err)
	}

	return oidc.Identity{
		Subject: subject,
		IDToken: "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature",
	}
}

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
