// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// MinPasswordLength is the shortest password SignUp accepts. Length is
// the only rule: NIST SP 800-63B section 5.1.1.2 asks verifiers to
// enforce a minimum of 8 characters and explicitly tells them not to
// impose composition rules, because those push people towards
// predictable substitutions rather than towards entropy. Twelve is the
// longer minimum that guidance also recommends where no other factor
// stands behind the password; here one does (TOTP), and twelve is still
// cheap to ask for.
const MinPasswordLength = 12

// MaxPasswordLength bounds what reaches Argon2id. The hash cost does not
// grow with the input, but reading an unbounded body into a memory-hard
// function on an unauthenticated route is not worth leaving open, and no
// passphrase a person types needs more room than this.
const MaxPasswordLength = 1024

// ErrInvalidCredentials is the one failure Local.Login returns: no such
// account, an account with no password or no TOTP secret, a disabled
// account, a wrong password, or a wrong, missing or replayed TOTP code
// all answer identically. Distinguishing any of them would tell an
// attacker which part of a guess was right; see Login.
var ErrInvalidCredentials = errors.New("auth: invalid email, password or totp code")

// ErrEmailInvalid, ErrPasswordTooShort, ErrPasswordTooLong and
// ErrEmailTaken are SignUp's refusals. Unlike a login, a signup can say
// which of them it was: the person is creating the account, so none of
// these tells them anything about somebody else's. ErrEmailTaken is the
// one that does say something about an existing account, and it is
// unavoidable: the email is unique across calbridge (see db/migrations),
// so a signup that collides has to be refused rather than silently
// turned into a login attempt.
var (
	ErrEmailInvalid     = errors.New("auth: email is not a valid address")
	ErrPasswordTooShort = fmt.Errorf("auth: password is shorter than %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("auth: password is longer than %d characters", MaxPasswordLength)
	ErrEmailTaken       = errors.New("auth: an account already exists for this email")
)

// dummyPasswordHash and dummyTOTPSecret are a well formed hash and
// secret no real account uses, computed once (argon2id hashing is
// deliberately expensive, so this is not redone on every login that
// turns out to be against a nonexistent account). Login compares against
// these when no usable account was found, so that case pays the same
// argon2id and HMAC cost a real wrong-password attempt does.
var (
	dummyPasswordHash = sync.OnceValue(func() string {
		hash, err := HashPassword("not-a-real-account-timing-equalizer-only")
		if err != nil {
			// HashPassword fails only if crypto/rand itself fails, which
			// means nothing on this machine can be trusted to run safely
			// at all. There is no reasonable value to return instead.
			panic(fmt.Errorf("auth: compute the dummy password hash: %w", err))
		}

		return hash
	})
	dummyTOTPSecret = sync.OnceValue(func() string {
		secret, err := GenerateTOTPSecret()
		if err != nil {
			panic(fmt.Errorf("auth: compute the dummy totp secret: %w", err))
		}

		return secret
	})
)

// Enrolment is what SignUp hands back for the person to put into an
// authenticator app. It is returned exactly once, by the call that
// created the account, and never read back out of the database
// afterwards: a secret an API will hand over again on request is not a
// second factor.
type Enrolment struct {
	// Secret is the base32 secret to type into an app that cannot scan.
	Secret string
	// URI is the otpauth:// URI the same secret encodes, for a QR code.
	URI string
}

// Local runs the local account path: signup with a password and a TOTP
// enrolment, and a login that checks both factors together.
type Local struct {
	store Store
	now   func() time.Time
}

// NewLocal builds a Local against store. now is the clock the TOTP check
// runs against; nil is time.Now.
func NewLocal(store Store, now func() time.Time) *Local {
	if now == nil {
		now = time.Now
	}

	return &Local{store: store, now: now}
}

// SignUp creates an active local account for email with password, enrols
// a fresh TOTP secret on it, and returns the identity together with the
// enrolment to show once.
//
// The account is usable the moment this returns, and a login against it
// needs the TOTP code the returned Enrolment produces, so a person who
// loses that response before scanning it has an account they cannot sign
// in to and has to create another under a different address. Password
// reset and recovery by mail are deliberately out of scope for this
// step (see the issue), and inventing a weaker recovery path to cover
// the gap would undo the second factor it exists to protect.
//
// No session is started here. Signing up and signing in are two steps,
// which is what makes the person prove the enrolment worked before they
// rely on it.
func (l *Local) SignUp(ctx context.Context, email, password string) (Identity, Enrolment, error) {
	email = strings.TrimSpace(email)
	if _, err := mail.ParseAddress(email); err != nil {
		return Identity{}, Enrolment{}, ErrEmailInvalid
	}

	switch {
	case len(password) < MinPasswordLength:
		return Identity{}, Enrolment{}, ErrPasswordTooShort
	case len(password) > MaxPasswordLength:
		return Identity{}, Enrolment{}, ErrPasswordTooLong
	}

	hash, err := HashPassword(password)
	if err != nil {
		return Identity{}, Enrolment{}, err
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		return Identity{}, Enrolment{}, err
	}

	user, err := l.store.Users().Create(ctx, storage.User{
		Email:        email,
		PasswordHash: &hash,
		TOTPSecret:   &secret,
		Status:       storage.UserStatusActive,
	})
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return Identity{}, Enrolment{}, ErrEmailTaken
		}

		return Identity{}, Enrolment{}, fmt.Errorf("auth: create local account: %w", err)
	}

	return identityFromUser(user), Enrolment{
		Secret: secret,
		URI:    TOTPProvisioningURI(secret, email),
	}, nil
}

// Login verifies email, password and totpCode together and returns the
// resulting Identity. Every failure, whichever step actually caused it,
// is ErrInvalidCredentials: the caller, and the person on the other end
// of the request, learns only that the attempt failed.
//
// The password and TOTP checks always run, even when no account was
// found at all, against dummyPasswordHash and dummyTOTPSecret instead of
// a real account's values. Skipping them for a nonexistent account would
// make that case answer faster than a wrong-password attempt against a
// real one, which is itself the signal an attacker needs to enumerate
// which addresses have an account; running the same argon2id hash and
// the same HMAC comparisons regardless keeps the two cases taking the
// same shape of work.
func (l *Local) Login(ctx context.Context, email, password, totpCode string) (Identity, error) {
	user, found, err := l.findLoginCandidate(ctx, email)
	if err != nil {
		return Identity{}, err
	}

	passwordHash := dummyPasswordHash()
	totpSecret := dummyTOTPSecret()

	if found {
		passwordHash = *user.PasswordHash
		totpSecret = *user.TOTPSecret
	}

	// Both checks run unconditionally, and neither short-circuits the
	// other: see the method doc.
	passwordOK, _ := VerifyPassword(passwordHash, password)
	totpCounter, totpOK := VerifyTOTPCode(totpSecret, totpCode, l.now())

	// A code that otherwise matches is still refused if it is not newer
	// than the last one this account already consumed: RFC 6238 section
	// 5.2 requires a verifier to reject a second use of an already
	// validated one-time password, so the exact same code, observed over
	// someone's shoulder or captured in transit, cannot be replayed for
	// the rest of its up-to-90-second skew window. This is gated on
	// found, which the function already branches on above, so it adds no
	// timing signal beyond the one that already separates a real account
	// from a nonexistent one.
	if found && totpOK && user.TOTPLastCounter != nil && totpCounter <= uint64(*user.TOTPLastCounter) {
		totpOK = false
	}

	if !found || !passwordOK || !totpOK {
		return Identity{}, ErrInvalidCredentials
	}

	matchedCounter := int64(totpCounter)
	user.TOTPLastCounter = &matchedCounter

	if err := l.store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		updated, err := l.store.Users().Update(ctx, user)
		if err != nil {
			return err
		}

		user = updated

		return nil
	}); err != nil {
		return Identity{}, fmt.Errorf("auth: persist totp counter: %w", err)
	}

	return identityFromUser(user), nil
}

// findLoginCandidate looks email up and reports whether the row behind
// it can be logged into at all: active, with both a password hash and a
// TOTP secret set. An account created through provider sign-in alone has
// neither, and must not become loggable by password just because
// somebody guessed its address.
//
// A lookup that matches nothing is not an error here. It is the ordinary
// case of a wrong address, and Login equalizes it against a real
// account's cost rather than returning early; a real failure of the
// database still comes back as one.
func (l *Local) findLoginCandidate(ctx context.Context, email string) (storage.User, bool, error) {
	user, err := l.store.Users().FindByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.User{}, false, nil
		}

		return storage.User{}, false, fmt.Errorf("auth: look up local account: %w", err)
	}

	usable := user.Status == storage.UserStatusActive &&
		user.PasswordHash != nil && user.TOTPSecret != nil

	return user, usable, nil
}
