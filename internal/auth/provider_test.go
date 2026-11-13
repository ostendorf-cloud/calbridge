// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	govueoidc "github.com/ostendorf-cloud/govue/oidc"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// calendarSubject is the provider account used in both roles in the
// linking rule's test below: once as the account somebody connected as a
// calendar, once as the subject of a sign-in. They are deliberately the
// same string, because the rule is about exactly that collision.
const calendarSubject = "google-account-108423"

// TestASignInIsNeverMatchedAgainstAnotherUsersCalendarConnection is the
// test behind this package's security boundary, and it asserts two
// things that have to hold together.
//
// A user already exists who connected a Google calendar for the provider
// account calendarSubject. Somebody then signs in with Google and the
// identity provider vouches for that same account. The sign-in must not
// land in the existing user's account: it gets one of its own.
//
// The second assertion is what keeps the first from passing by accident.
// The fake store counts every read of its connection repository, and the
// count has to still be zero afterwards: the sign-in path must not have
// consulted the connection at all, not even to decide against it. A
// check that looked and then declined would pass the first assertion and
// fail this one, and so would a lookup added later for some unrelated
// reason.
//
// Without this rule, controlling a mailbox whose calendar somebody
// connected would be a way into that person's calbridge account.
func TestASignInIsNeverMatchedAgainstAnotherUsersCalendarConnection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	// The person who connected a Google calendar, and the connection
	// itself, naming the very provider account the sign-in below is for.
	owner := createUser(ctx, t, store, "owner@example.com")

	if err := store.WithUser(ctx, owner.ID, func(ctx context.Context) error {
		_, err := store.Connections().Create(ctx, storage.Connection{
			UserID:      owner.ID,
			Provider:    storage.ConnectionProviderGoogle,
			DisplayName: calendarSubject,
			Credentials: []byte("refresh-token-for-the-connected-calendar"),
		})

		return err
	}); err != nil {
		t.Fatalf("seed the calendar connection: %v", err)
	}

	before := store.ConnectionReads

	identity, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle, govueoidc.Identity{
		Subject: calendarSubject,
		Email:   "stranger@example.com",
	})
	if err != nil {
		t.Fatalf("SignIn() error = %v, want the sign-in to succeed as its own account", err)
	}

	if identity.UserID == owner.ID {
		t.Fatal("the sign-in resolved to the account that had connected this provider account as a calendar; " +
			"a provider identity must never be matched against a calendar connection")
	}

	if got := store.ConnectionReads - before; got != 0 {
		t.Fatalf("the sign-in read the connection repository %d time(s), want 0: "+
			"resolving a sign-in must not consult a calendar connection at all", got)
	}

	if identity.Email != "stranger@example.com" {
		t.Fatalf("Email = %q, want the new account to carry the sign-in's own address", identity.Email)
	}
}

// TestSignInCreatesTheAccountOnFirstUseAndReusesItAfterwards covers the
// ordinary path: a subject nobody has seen gets an account, and the same
// subject coming back gets that same account rather than a second one.
func TestSignInCreatesTheAccountOnFirstUseAndReusesItAfterwards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	verified := govueoidc.Identity{Subject: "microsoft-subject-1", Email: "person@example.com"}

	first, err := auth.SignIn(ctx, store, storage.IdentityProviderMicrosoft, verified)
	if err != nil {
		t.Fatalf("first SignIn() error = %v", err)
	}

	second, err := auth.SignIn(ctx, store, storage.IdentityProviderMicrosoft, verified)
	if err != nil {
		t.Fatalf("second SignIn() error = %v", err)
	}

	if first.UserID != second.UserID {
		t.Fatalf("second sign-in resolved to %s, want the first sign-in's own account %s",
			second.UserID, first.UserID)
	}

	if got := store.CountUsers(); got != 1 {
		t.Fatalf("users = %d, want 1: the same subject must not create a second account", got)
	}

	// The account carries no password and no TOTP secret: a provider
	// sign-in sets neither, so nothing can log into it by password.
	user, ok := store.User(first.UserID)
	if !ok {
		t.Fatalf("no account %s in the store", first.UserID)
	}

	if user.PasswordHash != nil || user.TOTPSecret != nil {
		t.Fatal("an account created by provider sign-in carries a password hash or a totp secret, want neither")
	}
}

// TestSignInWithTheSameSubjectAtTwoProvidersCreatesTwoAccounts holds the
// pairing down: an identity is a (provider, subject) pair, so the same
// opaque string at two providers is two different people as far as this
// package is concerned.
func TestSignInWithTheSameSubjectAtTwoProvidersCreatesTwoAccounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	google, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle,
		govueoidc.Identity{Subject: "shared-subject", Email: "one@example.com"})
	if err != nil {
		t.Fatalf("google SignIn() error = %v", err)
	}

	microsoft, err := auth.SignIn(ctx, store, storage.IdentityProviderMicrosoft,
		govueoidc.Identity{Subject: "shared-subject", Email: "two@example.com"})
	if err != nil {
		t.Fatalf("microsoft SignIn() error = %v", err)
	}

	if google.UserID == microsoft.UserID {
		t.Fatal("the same subject at two providers resolved to one account, want two")
	}
}

// TestSignInRefusesAnEmailThatBelongsToAnotherAccount: an email claim is
// not proof of owning a calbridge account, so a first-time sign-in never
// adopts an existing one on the strength of it.
func TestSignInRefusesAnEmailThatBelongsToAnotherAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	existing := createUser(ctx, t, store, "taken@example.com")

	_, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle,
		govueoidc.Identity{Subject: "a-subject-nobody-has-seen", Email: "taken@example.com"})

	if !errors.Is(err, auth.ErrEmailBelongsToAnotherAccount) {
		t.Fatalf("SignIn() error = %v, want ErrEmailBelongsToAnotherAccount", err)
	}

	if got := store.CountIdentities(); got != 0 {
		t.Fatalf("identities = %d, want 0: a refused sign-in must bind nothing to the existing account", got)
	}

	if _, ok := store.User(existing.ID); !ok {
		t.Fatal("the existing account is gone after a refused sign-in")
	}
}

// TestSignInRefusesADisabledAccount: the account behind a known identity
// still has to be active, so disabling one actually stops its provider
// sign-in too.
func TestSignInRefusesADisabledAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	identity, err := auth.SignIn(ctx, store, storage.IdentityProviderGoogle,
		govueoidc.Identity{Subject: "subject-to-disable", Email: "disabled@example.com"})
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	disableAccount(t, store, identity.UserID)

	_, err = auth.SignIn(ctx, store, storage.IdentityProviderGoogle,
		govueoidc.Identity{Subject: "subject-to-disable", Email: "disabled@example.com"})

	if !errors.Is(err, auth.ErrAccountDisabled) {
		t.Fatalf("SignIn() error = %v, want ErrAccountDisabled", err)
	}
}

// TestAppleSignInGetsAReservedPlaceholderAddress: Apple is asked for the
// openid scope alone (see the package's identityScopes), so its ID token
// carries no email claim and the account needs an address users.email's
// own constraint accepts. It has to be under RFC 2606's reserved
// .invalid top level domain, so nothing can ever be delivered to it, and
// it has to be stable, so the same Apple account does not get a second
// address on its next sign-in.
func TestAppleSignInGetsAReservedPlaceholderAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()

	identity, err := auth.SignIn(ctx, store, storage.IdentityProviderApple,
		govueoidc.Identity{Subject: "apple-subject-000123"})
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	if !strings.HasSuffix(identity.Email, ".invalid") {
		t.Fatalf("Email = %q, want an address under the reserved .invalid top level domain", identity.Email)
	}

	if strings.Contains(identity.Email, "apple-subject-000123") {
		t.Fatalf("Email = %q, want the provider's subject not to appear in it", identity.Email)
	}

	again, err := auth.SignIn(ctx, store, storage.IdentityProviderApple,
		govueoidc.Identity{Subject: "apple-subject-000123"})
	if err != nil {
		t.Fatalf("second SignIn() error = %v", err)
	}

	if again.UserID != identity.UserID {
		t.Fatal("the second Apple sign-in created a second account, want the first one back")
	}
}

// TestSignInRefusesAVerifiedIdentityWithNoSubject guards the one input
// this package cannot do anything sensible with.
func TestSignInRefusesAVerifiedIdentityWithNoSubject(t *testing.T) {
	t.Parallel()

	store := storagetest.NewStore()

	_, err := auth.SignIn(context.Background(), store, storage.IdentityProviderGoogle,
		govueoidc.Identity{Email: "no-subject@example.com"})
	if err == nil {
		t.Fatal("SignIn() error = nil, want a refusal for an identity carrying no subject")
	}

	if got := store.CountUsers(); got != 0 {
		t.Fatalf("users = %d, want 0", got)
	}
}

// TestNewProviderAsksForIdentityScopesOnly is the scope half of the
// consent split: sign-in asks for identity and never for a calendar.
// Scopes are not configurable, so this asserts what NewProvider builds
// rather than what a caller passed, by reading the authorization URL the
// provider produces.
func TestNewProviderAsksForIdentityScopesOnly(t *testing.T) {
	t.Parallel()

	for _, id := range []storage.IdentityProvider{
		storage.IdentityProviderGoogle,
		storage.IdentityProviderMicrosoft,
		storage.IdentityProviderApple,
	} {
		t.Run(string(id), func(t *testing.T) {
			t.Parallel()

			provider, err := auth.NewProvider(id, auth.ProviderConfig{
				ClientID:     "a-client-id",
				ClientSecret: "a-client-secret",
				RedirectURL:  "https://calbridge.example.com/api/v1/auth/providers/" + string(id) + "/callback",
			})
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}

			scopes := auth.ScopesFor(provider)

			if !slices.Contains(scopes, "openid") {
				t.Fatalf("scopes = %v, want openid among them", scopes)
			}

			for _, scope := range scopes {
				if strings.Contains(strings.ToLower(scope), "calendar") {
					t.Fatalf("scopes = %v, want no calendar scope: signing in and connecting a calendar "+
						"are two separate consent steps", scopes)
				}
			}

			if id == storage.IdentityProviderApple && !slices.Equal(scopes, []string{"openid"}) {
				t.Fatalf("apple scopes = %v, want openid alone: anything more makes Apple answer the "+
					"callback with a POST, which govue's oidc callback refuses", scopes)
			}
		})
	}
}

// TestAppleIsTheProviderDocumentedAsIdentityOnly: the login screen
// renders its note from this flag, and the issue requires that note.
func TestAppleIsTheProviderDocumentedAsIdentityOnly(t *testing.T) {
	t.Parallel()

	tests := map[storage.IdentityProvider]bool{
		storage.IdentityProviderGoogle:    false,
		storage.IdentityProviderMicrosoft: false,
		storage.IdentityProviderApple:     true,
	}

	for id, wantIdentityOnly := range tests {
		t.Run(string(id), func(t *testing.T) {
			t.Parallel()

			provider, err := auth.NewProvider(id, auth.ProviderConfig{
				ClientID:     "a-client-id",
				ClientSecret: "a-client-secret",
				RedirectURL:  "https://calbridge.example.com/callback",
			})
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}

			if got := provider.Info().IdentityOnly; got != wantIdentityOnly {
				t.Fatalf("IdentityOnly = %t, want %t", got, wantIdentityOnly)
			}
		})
	}
}

// TestNewProviderRefusesAnUnknownProvider keeps a typo in configuration
// from becoming a half-built sign-in button.
func TestNewProviderRefusesAnUnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := auth.NewProvider("dropbox", auth.ProviderConfig{
		ClientID:     "a-client-id",
		ClientSecret: "a-client-secret",
		RedirectURL:  "https://calbridge.example.com/callback",
	})

	if !errors.Is(err, auth.ErrUnknownProvider) {
		t.Fatalf("NewProvider() error = %v, want ErrUnknownProvider", err)
	}
}
