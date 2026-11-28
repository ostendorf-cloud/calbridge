// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

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
		// context.Background, not a request's: this runs once, at the
		// first login the process serves, and a request that happens to
		// be cancelled while it runs must not leave the package without
		// the one value every later failed login is compared against.
		hash, err := HashPassword(context.Background(), "not-a-real-account-timing-equalizer-only")
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
	opts  options
}

// NewLocal builds a Local against store. now is the clock the TOTP check
// runs against; nil is time.Now. Registration is open unless opts close
// it; see WithRegistrationClosed.
func NewLocal(store Store, now func() time.Time, opts ...Option) *Local {
	if now == nil {
		now = time.Now
	}

	return &Local{store: store, now: now, opts: newOptions(opts)}
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
	// First, before the input is looked at: a closed instance answers the
	// same way whatever was sent, so the refusal says nothing about which
	// addresses already have an account, and no Argon2id derivation runs
	// for a request that cannot succeed.
	if l.opts.registrationClosed {
		return Identity{}, Enrolment{}, ErrRegistrationClosed
	}

	email = strings.TrimSpace(email)
	if _, err := mail.ParseAddress(email); err != nil {
		return Identity{}, Enrolment{}, ErrEmailInvalid
	}

	// PlaceholderEmailDomain is this application's own namespace, not an
	// address anybody owns, and mail.ParseAddress has no reason to think
	// otherwise. A local signup allowed to pick one could claim the slot
	// a particular provider account will land in before that person ever
	// signs in, which would lock them out of the provider they are
	// entitled to use; see signInEmail.
	if isPlaceholderEmail(email) {
		return Identity{}, Enrolment{}, ErrEmailInvalid
	}

	switch {
	case len(password) < MinPasswordLength:
		return Identity{}, Enrolment{}, ErrPasswordTooShort
	case len(password) > MaxPasswordLength:
		return Identity{}, Enrolment{}, ErrPasswordTooLong
	}

	hash, err := HashPassword(ctx, password)
	if err != nil {
		return Identity{}, Enrolment{}, err
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		return Identity{}, Enrolment{}, err
	}

	// Built before the account is created, not after: everything that
	// can fail should fail while there is still no row to leave behind.
	// email passed mail.ParseAddress above, so a refusal here means this
	// package and that parser disagree about what an address can hold,
	// which is a bug rather than anything the person typed.
	uri, err := TOTPProvisioningURI(secret, email)
	if err != nil {
		return Identity{}, Enrolment{}, fmt.Errorf("auth: build the totp provisioning uri: %w", err)
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
		URI:    uri,
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
	passwordOK, verifyErr := VerifyPassword(ctx, passwordHash, password)
	if errors.Is(verifyErr, ErrHashingBusy) {
		// Not a failed login: this server had no derivation slot free.
		// Answering ErrInvalidCredentials here would tell somebody with
		// the right password that it is wrong, and would let a burst of
		// traffic look like a wave of bad passwords in the log.
		return Identity{}, verifyErr
	}

	totpCounter, totpOK := VerifyTOTPCode(totpSecret, totpCode, l.now())

	// A code that otherwise matches is still refused if it is not newer
	// than the last one this account already consumed: RFC 6238 section
	// 5.2 requires a verifier to reject a second use of an already
	// validated one-time password, so the exact same code, observed over
	// someone's shoulder or captured in transit, cannot be replayed for
	// the rest of its up-to-90-second skew window.
	//
	// This comparison, against the value read before the roughly 0.3
	// second password verify, is the cheap half. It is not what actually
	// holds the rule: two logins racing with the same captured code both
	// read the same counter and both pass here. The advance below is
	// where a replay is really refused, in one statement, against the
	// value the row holds at that moment. This half stays because it
	// rejects the common case without a write, and because it is gated
	// on found, which the function already branches on, so it adds no
	// timing signal of its own.
	if found && totpOK && user.TOTPLastCounter != nil && totpCounter <= uint64(*user.TOTPLastCounter) {
		totpOK = false
	}

	if !found || !passwordOK || !totpOK {
		return Identity{}, ErrInvalidCredentials
	}

	if err := l.consumeTOTPCode(ctx, user.ID, int64(totpCounter)); err != nil {
		return Identity{}, err
	}

	return identityFromUser(user), nil
}

// consumeTOTPCode advances the account's last accepted RFC 6238 counter
// to counter, and refuses the login when it cannot.
//
// The write is conditional on the counter's own previous value (see
// storage.UserRepository.AdvanceTOTPCounter), which is what makes this
// the real replay check: of two logins racing with the same code, the
// database lets exactly one advance the counter, and the other is told
// no rows changed and answers like any other wrong code. Checking in Go
// and then writing could not do that, because the two steps run on two
// different connections with another request's write free to land
// between them.
func (l *Local) consumeTOTPCode(ctx context.Context, userID uuid.UUID, counter int64) error {
	err := l.store.WithUser(ctx, userID, func(ctx context.Context) error {
		return l.store.Users().AdvanceTOTPCounter(ctx, userID, counter)
	})

	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound):
		// Either this code was already consumed, by a request that got
		// there first, or the account went away between the lookup and
		// here. Both are refusals, and both look to the caller exactly
		// like a wrong code.
		return ErrInvalidCredentials
	default:
		return fmt.Errorf("auth: consume totp code: %w", err)
	}
}

// isPlaceholderEmail reports whether email sits in the namespace
// signInEmail hands out. The comparison is on the domain alone and is
// case insensitive, matching how users.email is made unique (see
// db/migrations), so neither a different local part nor a different case
// gets around it.
func isPlaceholderEmail(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}

	return strings.EqualFold(email[at+1:], PlaceholderEmailDomain)
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
