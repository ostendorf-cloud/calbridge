// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// TestSignUpOnAClosedInstanceCreatesNoAccount covers the local path. The
// refusal comes before the address is looked at, so even a request that
// would have been refused for another reason gets the closed answer.
func TestSignUpOnAClosedInstanceCreatesNoAccount(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		email, password string
	}{
		"a valid address":  {"person@example.com", goodPassword},
		"an invalid email": {"not-an-address", goodPassword},
		"a short password": {"person@example.com", "short"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := storagetest.NewStore()
			local := auth.NewLocal(store, fixedNow, auth.WithRegistrationClosed())

			_, _, err := local.SignUp(context.Background(), test.email, test.password)
			if !errors.Is(err, auth.ErrRegistrationClosed) {
				t.Fatalf("SignUp() error = %v, want ErrRegistrationClosed", err)
			}

			if got := store.CountUsers(); got != 0 {
				t.Fatalf("users = %d, want 0 after a refused signup", got)
			}
		})
	}
}

// TestSignUpIsOpenWhenNoOptionIsGiven pins the default: a deployment that
// configures nothing keeps accepting signups.
func TestSignUpIsOpenWhenNoOptionIsGiven(t *testing.T) {
	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	if _, _, err := local.SignUp(ctx, "person@example.com", goodPassword); err != nil {
		t.Fatalf("SignUp() error = %v, want an open instance by default", err)
	}

	if got := store.CountUsers(); got != 1 {
		t.Fatalf("users = %d, want 1", got)
	}
}

// TestAnExistingAccountStillLogsInOnAClosedInstance proves the switch
// closes registration and nothing else.
func TestAnExistingAccountStillLogsInOnAClosedInstance(t *testing.T) {
	ctx := context.Background()
	store := storagetest.NewStore()

	_, enrolment, err := auth.NewLocal(store, fixedNow).SignUp(ctx, "person@example.com", goodPassword)
	if err != nil {
		t.Fatalf("SignUp() on the open instance error = %v", err)
	}

	code, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("GenerateTOTPCode() error = %v", err)
	}

	closed := auth.NewLocal(store, fixedNow, auth.WithRegistrationClosed())
	if _, err := closed.Login(ctx, "person@example.com", goodPassword, code); err != nil {
		t.Fatalf("Login() on the closed instance error = %v", err)
	}
}

// TestProviderSignInOnAClosedInstanceCreatesNoAccount covers the second
// registration path: a provider sign-in creates the account on first use,
// so it needs the same refusal as the signup route.
func TestProviderSignInOnAClosedInstanceCreatesNoAccount(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		provider storage.IdentityProvider
		subject  string
		email    string
		verified bool
	}{
		"a verified address":    {storage.IdentityProviderGoogle, "google-subject-1", "person@example.com", true},
		"an unverified address": {storage.IdentityProviderMicrosoft, "microsoft-subject-1", "person@example.com", false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := storagetest.NewStore()
			identity := signedInAs(t, test.subject, test.email, test.verified)

			_, err := auth.SignIn(context.Background(), store, test.provider, identity, auth.WithRegistrationClosed())
			if !errors.Is(err, auth.ErrRegistrationClosed) {
				t.Fatalf("SignIn() error = %v, want ErrRegistrationClosed", err)
			}

			if got := store.CountUsers(); got != 0 {
				t.Fatalf("users = %d, want 0 after a refused sign-in", got)
			}

			if got := store.CountIdentities(); got != 0 {
				t.Fatalf("identities = %d, want 0 after a refused sign-in", got)
			}
		})
	}
}

// TestAProviderAccountStillSignsInOnAClosedInstance: an identity that was
// linked while the instance was open keeps working after it closes.
func TestAProviderAccountStillSignsInOnAClosedInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	identity := signedInAs(t, "google-subject-1", "person@example.com", true)

	first, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle, identity)
	if err != nil {
		t.Fatalf("SignIn() on the open instance error = %v", err)
	}

	again, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle, identity, auth.WithRegistrationClosed())
	if err != nil {
		t.Fatalf("SignIn() on the closed instance error = %v", err)
	}

	if again.UserID != first.UserID {
		t.Fatalf("closed sign-in resolved to %s, want the existing account %s", again.UserID, first.UserID)
	}

	if got := store.CountUsers(); got != 1 {
		t.Fatalf("users = %d, want 1", got)
	}
}
