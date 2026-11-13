// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// testNow is the fixed clock every local login test runs against, so a
// TOTP step boundary can never make one of them flaky.
var testNow = time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

// goodPassword is long enough for auth.MinPasswordLength and obviously
// not a credential for anything.
const goodPassword = "a-long-enough-test-passphrase"

func fixedNow() time.Time { return testNow }

// TestSignUpThenLoginWithBothFactors is the whole local path end to end:
// an account is created, its enrolment produces a code, and the login
// takes the password and that code together.
func TestSignUpThenLoginWithBothFactors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	identity, enrolment, err := local.SignUp(ctx, "person@example.com", goodPassword)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}

	if enrolment.Secret == "" || !strings.HasPrefix(enrolment.URI, "otpauth://totp/") {
		t.Fatalf("enrolment = %+v, want a secret and an otpauth uri", enrolment)
	}

	code, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("GenerateTOTPCode() error = %v", err)
	}

	loggedIn, err := local.Login(ctx, "person@example.com", goodPassword, code)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if loggedIn.UserID != identity.UserID {
		t.Fatalf("Login() resolved to %s, want the account signup created, %s", loggedIn.UserID, identity.UserID)
	}

	// The stored password is a hash, never the password itself.
	stored, ok := store.User(identity.UserID)
	if !ok {
		t.Fatalf("no account %s in the store after signup", identity.UserID)
	}

	if stored.PasswordHash == nil || *stored.PasswordHash == goodPassword {
		t.Fatal("the stored password hash is missing or is the password in clear")
	}
}

// TestLoginRefusesTheSameCodeTwice is RFC 6238 section 5.2: a code is
// good once, so one observed over a shoulder cannot be replayed for the
// rest of its skew window.
func TestLoginRefusesTheSameCodeTwice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	_, enrolment, err := local.SignUp(ctx, "person@example.com", goodPassword)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}

	code, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("GenerateTOTPCode() error = %v", err)
	}

	if _, err := local.Login(ctx, "person@example.com", goodPassword, code); err != nil {
		t.Fatalf("first Login() error = %v", err)
	}

	_, err = local.Login(ctx, "person@example.com", goodPassword, code)
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("second Login() error = %v, want ErrInvalidCredentials for a replayed code", err)
	}
}

// TestLoginRefusalsAreIndistinguishable: a wrong password, a wrong code
// and an address with no account behind it all answer with the identical
// error, so nothing here tells an attacker which part of a guess was
// right.
func TestLoginRefusalsAreIndistinguishable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	_, enrolment, err := local.SignUp(ctx, "person@example.com", goodPassword)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}

	validCode, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("GenerateTOTPCode() error = %v", err)
	}

	tests := map[string]struct{ email, password, code string }{
		"no account for this address": {"nobody@example.com", goodPassword, validCode},
		"wrong password":              {"person@example.com", "another-long-passphrase", validCode},
		"wrong code":                  {"person@example.com", goodPassword, "000000"},
		"no code at all":              {"person@example.com", goodPassword, ""},
		"empty everything":            {"", "", ""},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := local.Login(ctx, test.email, test.password, test.code)
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

// TestLoginRefusesAnAccountWithNoPasswordOfItsOwn: an account created
// through provider sign-in has no password and no TOTP secret, and must
// not become loggable by password because somebody guessed its address.
func TestLoginRefusesAnAccountWithNoPasswordOfItsOwn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	createUser(ctx, t, store, "provider-only@example.com")

	_, err := local.Login(ctx, "provider-only@example.com", goodPassword, "123456")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

// TestLoginRefusesADisabledAccount: disabling an account has to stop its
// password login too, not only its provider sign-in.
func TestLoginRefusesADisabledAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	identity, enrolment, err := local.SignUp(ctx, "person@example.com", goodPassword)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}

	disableAccount(t, store, identity.UserID)

	code, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("GenerateTOTPCode() error = %v", err)
	}

	_, err = local.Login(ctx, "person@example.com", goodPassword, code)
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestSignUpRefusals(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		email, password string
		want            error
	}{
		"no at sign":         {"not-an-address", goodPassword, auth.ErrEmailInvalid},
		"empty email":        {"", goodPassword, auth.ErrEmailInvalid},
		"password too short": {"person@example.com", strings.Repeat("x", auth.MinPasswordLength-1), auth.ErrPasswordTooShort},
		"password too long":  {"person@example.com", strings.Repeat("x", auth.MaxPasswordLength+1), auth.ErrPasswordTooLong},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := storagetest.NewStore()
			local := auth.NewLocal(store, fixedNow)

			_, _, err := local.SignUp(context.Background(), test.email, test.password)
			if !errors.Is(err, test.want) {
				t.Fatalf("SignUp() error = %v, want %v", err, test.want)
			}

			if got := store.CountUsers(); got != 0 {
				t.Fatalf("users = %d, want 0 after a refused signup", got)
			}
		})
	}
}

// TestSignUpRefusesAnAddressThatAlreadyHasAnAccount: the email is unique
// across calbridge, so a collision has to be refused rather than turned
// into a login attempt against somebody else's account.
func TestSignUpRefusesAnAddressThatAlreadyHasAnAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := storagetest.NewStore()
	local := auth.NewLocal(store, fixedNow)

	if _, _, err := local.SignUp(ctx, "person@example.com", goodPassword); err != nil {
		t.Fatalf("first SignUp() error = %v", err)
	}

	_, _, err := local.SignUp(ctx, "PERSON@example.com", goodPassword)
	if !errors.Is(err, auth.ErrEmailTaken) {
		t.Fatalf("second SignUp() error = %v, want ErrEmailTaken, matched without regard to case", err)
	}
}

// TestSignUpReportsAStoreFailureAsItself: a database that is down is an
// operational problem, not a refusal to show the person as a rule they
// broke.
func TestSignUpReportsAStoreFailureAsItself(t *testing.T) {
	t.Parallel()

	store := storagetest.NewStore()
	store.FailUserCreate = errStoreUnavailable
	local := auth.NewLocal(store, fixedNow)

	_, _, err := local.SignUp(context.Background(), "person@example.com", goodPassword)
	if !errors.Is(err, errStoreUnavailable) {
		t.Fatalf("SignUp() error = %v, want the store's own failure", err)
	}

	for _, sentinel := range []error{auth.ErrEmailInvalid, auth.ErrEmailTaken, auth.ErrPasswordTooShort} {
		if errors.Is(err, sentinel) {
			t.Fatalf("SignUp() error = %v, want it not to look like %v", err, sentinel)
		}
	}
}
