// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// TestDeleteAccountRemovesTheAccountAndWhatItOwns: the account, its
// sign-in identities and its calendar connections all go, and what
// another account owns stays.
func TestDeleteAccountRemovesTheAccountAndWhatItOwns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	leaving, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle,
		signedInAs(t, "leaving-subject", "leaving@example.com", true))
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	if err := store.WithUser(ctx, leaving.UserID, func(ctx context.Context) error {
		_, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      leaving.UserID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: "a connected calendar",
			Credentials: []byte("a refresh token"),
		})

		return err
	}); err != nil {
		t.Fatalf("seed the connection: %v", err)
	}

	staying := createUser(ctx, t, store, "staying@example.com")

	if err := auth.DeleteAccount(ctx, store, leaving.UserID); err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}

	if _, ok := store.User(leaving.UserID); ok {
		t.Fatal("the account is still there after DeleteAccount")
	}

	if got := store.CountIdentities(); got != 0 {
		t.Fatalf("identities = %d, want 0: the account's sign-in identities must go with it", got)
	}

	if got := store.CountConnections(); got != 0 {
		t.Fatalf("connections = %d, want 0: the account's calendar connections must go with it", got)
	}

	if _, ok := store.User(staying.ID); !ok {
		t.Fatal("another account was deleted too")
	}
}

// TestDeleteAccountFreesTheAddressAndTheIdentity: once an account is
// gone, the same person can sign up again under the same address, and
// the same provider subject gets a fresh account rather than finding a
// dangling identity.
func TestDeleteAccountFreesTheAddressAndTheIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	verified := signedInAs(t, "returning-subject", "returning@example.com", true)

	first, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle, verified)
	if err != nil {
		t.Fatalf("first SignIn() error = %v", err)
	}

	if err := auth.DeleteAccount(ctx, store, first.UserID); err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}

	second, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle, verified)
	if err != nil {
		t.Fatalf("second SignIn() error = %v", err)
	}

	if second.UserID == first.UserID {
		t.Fatal("the second sign-in reused the deleted account's id, want a new account")
	}
}

func TestDeleteAccountReportsAnAccountThatIsNotThere(t *testing.T) {
	t.Parallel()

	err := auth.DeleteAccount(context.Background(), storagetest.NewStore(), uuid.New())
	if !errors.Is(err, auth.ErrAccountNotFound) {
		t.Fatalf("DeleteAccount() error = %v, want ErrAccountNotFound", err)
	}
}
