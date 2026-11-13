// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/oidc"
	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
)

// maxCredentialBody bounds what a signup or a login may send. Both
// bodies are a handful of short strings, and auth.MaxPasswordLength is
// the largest of them; this leaves generous room above it while keeping
// an unauthenticated route from reading anything large into memory.
const maxCredentialBody = 8 << 10

// signUpRequest is handleSignUp's body.
type signUpRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// signUpResponse is what a successful signup answers with: the TOTP
// secret and the otpauth URI encoding it, shown to the person once and
// never readable again. Nothing here starts a session: signing up and
// signing in are two steps, which is what makes the person prove the
// enrolment worked before they depend on it.
type signUpResponse struct {
	TOTPSecret string `json:"totp_secret"`
	TOTPURI    string `json:"totp_uri"`
}

// loginRequest is handleLogin's body: both factors together, which is
// the one call internal/auth's Local.Login checks them in.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// meResponse is what handleMe answers a valid session with: who the
// session is for, and the CSRF token that same session's id derives, for
// the client to spend on its next unsafe request.
type meResponse struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	CSRFToken string `json:"csrf_token"`
}

// providersResponse is what the login screen builds its buttons from.
type providersResponse struct {
	Providers []providerResponse `json:"providers"`
}

// providerResponse is one sign-in button. IdentityOnly is what the UI
// renders the "grants identity only, connects no calendar" note from;
// see internal/auth's ProviderInfo.
type providerResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IdentityOnly bool   `json:"identity_only"`
}

// handleSignUp creates a local account and answers with its TOTP
// enrolment.
func (a *app) handleSignUp(w http.ResponseWriter, r *http.Request) error {
	var req signUpRequest
	if err := api.DecodeLimit(r, &req, maxCredentialBody); err != nil {
		return err
	}

	_, enrolment, err := a.local.SignUp(r.Context(), req.Email, req.Password)
	if err != nil {
		return signUpError(err)
	}

	return api.Write(w, http.StatusOK, signUpResponse{
		TOTPSecret: enrolment.Secret,
		TOTPURI:    enrolment.URI,
	})
}

// signUpError turns internal/auth's signup refusals into answers. Each
// of them says which rule the person broke, because the person is
// creating their own account and none of these tells them anything about
// somebody else's; see internal/auth's own reasoning on ErrEmailTaken.
func signUpError(err error) error {
	switch {
	case errors.Is(err, auth.ErrHashingBusy):
		return hashingBusyError()
	case errors.Is(err, auth.ErrEmailInvalid):
		return api.Invalid("enter a valid email address")
	case errors.Is(err, auth.ErrPasswordTooShort):
		return api.Invalid(fmt.Sprintf("the password must be at least %d characters long", auth.MinPasswordLength))
	case errors.Is(err, auth.ErrPasswordTooLong):
		return api.Invalid(fmt.Sprintf("the password must be at most %d characters long", auth.MaxPasswordLength))
	case errors.Is(err, auth.ErrEmailTaken):
		return api.Conflict("an account already exists for this email address")
	default:
		return fmt.Errorf("httpapi: sign up: %w", err)
	}
}

// loginError turns internal/auth's login refusals into answers. Every
// wrong credential answers identically, whichever part of the attempt
// was wrong: internal/auth collapses them into one error for exactly
// that reason, and this must not undo it by telling them apart again.
func loginError(err error) error {
	switch {
	case errors.Is(err, auth.ErrHashingBusy):
		// Deliberately not an authentication failure: the password was
		// never checked. Telling somebody with the right password that
		// it is wrong would be a lie, and would turn a load problem
		// into a support request about a broken account.
		return hashingBusyError()
	case errors.Is(err, auth.ErrInvalidCredentials):
		return api.Unauthenticated("the email, password or code is wrong")
	default:
		return fmt.Errorf("httpapi: log in: %w", err)
	}
}

// hashingBusyError is what a request shed by internal/auth's limit on
// concurrent password hashing is answered with: a 503, the honest answer
// to a server already running as many Argon2id derivations as its memory
// allows. See auth.MaxConcurrentPasswordHashes for why that limit
// exists and what happens without it.
func hashingBusyError() error {
	return api.Unavailable("too many sign-in attempts are being processed, please try again in a moment")
}

// handleLogin verifies both factors and starts a session.
//
// Every refusal answers identically, whichever step failed: internal/auth
// collapses them into one error for exactly that reason, and this handler
// must not undo it by distinguishing them again here.
func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := api.DecodeLimit(r, &req, maxCredentialBody); err != nil {
		return err
	}

	identity, err := a.local.Login(r.Context(), req.Email, req.Password, req.TOTPCode)
	if err != nil {
		return loginError(err)
	}

	if _, err := a.sessions.Start(r.Context(), w, identity.Session()); err != nil {
		return fmt.Errorf("httpapi: start session: %w", err)
	}

	api.NoContent(w)

	return nil
}

// handleMe answers the session a request carries, or api.Unauthenticated
// when it carries none.
func (a *app) handleMe(w http.ResponseWriter, r *http.Request) error {
	s, err := a.sessionFromRequest(r)
	if err != nil {
		return err
	}

	return api.Write(w, http.StatusOK, meResponse{
		UserID:    s.Subject,
		Email:     s.Email,
		CSRFToken: csrf.Token(a.csrfKey, s.ID),
	})
}

// handleLogout ends the session a request carries, if any. Reaching this
// handler at all already proved the CSRF token the request carried
// matched the session's own, since this route is behind the CSRF
// protector like every other.
func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if err := a.sessions.End(r.Context(), w, r); err != nil {
		return fmt.Errorf("httpapi: end session: %w", err)
	}

	api.NoContent(w)

	return nil
}

// handleDeleteAccount deletes the account the session is for, together
// with everything it owns, and then ends every session that account had
// open, not only the one this request carried: an account that no longer
// exists must not be reachable from a browser that still holds a cookie
// for it on another device.
//
// It is a DELETE on the same path GET /api/v1/me reads, so it is behind
// the CSRF protector like every other unsafe route, and the session it
// deletes is the only thing it will accept as proof of who is asking.
func (a *app) handleDeleteAccount(w http.ResponseWriter, r *http.Request) error {
	s, err := a.sessionFromRequest(r)
	if err != nil {
		return err
	}

	userID, err := uuid.Parse(s.Subject)
	if err != nil {
		// Every session this application starts carries a users row id as
		// its subject (see auth.Identity.Session), so anything else is a
		// session from a build that did not, not something the person can
		// fix by trying again.
		return fmt.Errorf("httpapi: delete account: session subject is not a user id: %w", err)
	}

	if err := auth.DeleteAccount(r.Context(), a.store, userID); err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return api.Unauthenticated("log in first")
		}

		return fmt.Errorf("httpapi: delete account: %w", err)
	}

	if err := a.sessions.EndAll(r.Context(), s.Subject); err != nil {
		return fmt.Errorf("httpapi: end the deleted account's sessions: %w", err)
	}

	if err := a.sessions.End(r.Context(), w, r); err != nil {
		return fmt.Errorf("httpapi: clear the deleted account's session cookie: %w", err)
	}

	api.NoContent(w)

	return nil
}

// handleProviders answers the sign-in providers this deployment
// configured. It carries no client id, no issuer and no URL: a button's
// label and the identity-only note are everything the login screen needs,
// and the rest is deployment configuration a browser has no use for.
//
// It needs no session: the login screen asks before anybody is signed in.
func (a *app) handleProviders(w http.ResponseWriter, _ *http.Request) error {
	providers := make([]providerResponse, 0, len(a.providers))
	for _, info := range a.providers {
		providers = append(providers, providerResponse{
			ID:           string(info.ID),
			Name:         info.Name,
			IdentityOnly: info.IdentityOnly,
		})
	}

	return api.Write(w, http.StatusOK, providersResponse{Providers: providers})
}

// providerSignInError is what a refused provider sign-in is answered
// with. govue's oidc package has already written the cause to the log by
// the time this runs and hands over one of its own two opaque errors, so
// nothing here reconstructs a reason: oidc.ErrProvider means the
// identity provider could not be reached and starting over will not
// help, and everything else means this one attempt is over.
func providerSignInError(err error) error {
	if errors.Is(err, oidc.ErrProvider) {
		return api.Unavailable("the sign-in service is unavailable, please try again later")
	}

	return api.Invalid("the sign-in could not be completed, please start over")
}

// sessionFromRequest is the one place a handler reads the session, so
// "no session" and "the store is broken" are told apart in one place
// rather than in each of them.
func (a *app) sessionFromRequest(r *http.Request) (session.Session, error) {
	s, ok, err := a.sessions.Read(r.Context(), r)
	if err != nil {
		return session.Session{}, api.Unavailable("session store is unavailable").WithCause(err)
	}

	if !ok {
		return session.Session{}, api.Unauthenticated("log in first")
	}

	return s, nil
}
