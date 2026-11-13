// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/govue/oidc"
	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// DefaultIssuer is the issuer URL each provider is reached at unless a
// deployment names another. Microsoft's is the multi-tenant "common"
// authority, which is what an application open to anyone needs; a
// deployment restricted to one Entra ID tenant sets its own
// tenant-specific issuer instead, which is the one case overriding this
// is meant for.
var DefaultIssuer = map[storage.IdentityProvider]string{
	storage.IdentityProviderGoogle:    "https://accounts.google.com",
	storage.IdentityProviderMicrosoft: "https://login.microsoftonline.com/common/v2.0",
	storage.IdentityProviderApple:     "https://appleid.apple.com",
}

// ProviderDisplayName is what a sign-in button says for each provider.
var ProviderDisplayName = map[storage.IdentityProvider]string{
	storage.IdentityProviderGoogle:    "Google",
	storage.IdentityProviderMicrosoft: "Microsoft",
	storage.IdentityProviderApple:     "Apple",
}

// identityScopes is what a sign-in asks each provider for, and the only
// thing it asks for. Neither the set nor its members are configurable:
// see ProviderConfig for why, and the package doc for the consent split
// this enforces.
//
// Google and Microsoft get OpenID Connect's own three standard identity
// scopes, which is oidc.DefaultScopes: openid identifies the flow, and
// profile and email carry the claims a person's account page needs. None
// of the three grants access to anything but the account's own profile,
// and in particular neither provider's calendar scopes
// (https://www.googleapis.com/auth/calendar..., Calendars.ReadWrite) is
// among them.
//
// Apple gets "openid" alone. Apple requires response_mode=form_post
// whenever the name or email scope is asked for, which answers the
// callback with a POST, and the callback govue's oidc package hands back
// answers anything but GET and HEAD with 405 (see its own onlyRead).
// Asking for less is the right side to err on for a sign-in that is
// documented to grant identity and nothing else; what it costs is the
// email claim, which signInEmail below covers.
var identityScopes = map[storage.IdentityProvider][]string{
	storage.IdentityProviderGoogle:    oidc.DefaultScopes,
	storage.IdentityProviderMicrosoft: oidc.DefaultScopes,
	storage.IdentityProviderApple:     {"openid"},
}

// ErrUnknownProvider is returned for a provider id this package does not
// know, or one a deployment did not configure.
var ErrUnknownProvider = errors.New("auth: unknown or unconfigured sign-in provider")

// ErrAccountDisabled is returned when a sign-in resolves to an account
// whose status is not active. It never reaches a browser as itself; see
// NewProviderHandlers.
var ErrAccountDisabled = errors.New("auth: this account is disabled")

// ErrEmailBelongsToAnotherAccount is returned when a first-time provider
// sign-in carries an email that already has a calbridge account behind
// it. The account is not adopted: an email claim is not proof of
// ownership of a calbridge account, and taking one over on the strength
// of it is the same mistake as matching a sign-in against somebody
// else's calendar connection. Linking a provider to an account the
// person is already signed in to is its own flow and its own issue.
var ErrEmailBelongsToAnotherAccount = errors.New("auth: an account already exists for this email")

// ProviderConfig is one provider's client registration. Issuer,
// ClientID, ClientSecret and RedirectURL are what a deployment supplies;
// everything else this package fixes.
//
// There is deliberately no Scopes field. A sign-in asks for identity
// scopes only (see identityScopes), and a deployment that could widen
// the list could ask a person for their calendar at the sign-in screen,
// which is exactly the consent split docs/concepts/calbridge.md keeps
// apart. GroupsClaim is absent for the same reason in reverse: calbridge
// authorizes nothing by provider group membership, so reading one would
// only be a claim to carry around and get wrong.
type ProviderConfig struct {
	// Issuer is the URL the discovery document lives under. Empty uses
	// DefaultIssuer for the provider.
	Issuer string

	// ClientID is this application's client at the provider.
	ClientID string

	// ClientSecret authenticates the token request. It comes from
	// internal/config, which reads it through envconf's _FILE convention,
	// and must never be logged or put in an error.
	ClientSecret string

	// RedirectURL is this application's callback for this provider, as
	// registered at the provider, which matches it exactly.
	RedirectURL string

	// HTTPClient and Now are test hooks, passed through to oidc.Config.
	// Both are nil, meaning the real client and the real clock, in
	// production.
	HTTPClient *http.Client
	Now        func() time.Time
}

// Provider is one configured sign-in provider: its calbridge id and the
// govue oidc.Provider behind it.
type Provider struct {
	id       storage.IdentityProvider
	provider *oidc.Provider
	// scopes is exactly what was handed to oidc.New, kept so a test can
	// assert on what this sign-in asks for without a network round trip
	// to the provider's authorization endpoint.
	scopes []string
}

// ID is the provider this is a sign-in for.
func (p *Provider) ID() storage.IdentityProvider { return p.id }

// ProviderInfo is what internal/httpapi hands the UI so it can offer the
// right buttons: it carries no secret and no URL, only what a button
// needs to say.
type ProviderInfo struct {
	ID   storage.IdentityProvider
	Name string
	// IdentityOnly marks a provider that grants identity and nothing
	// else, so the UI can say so where the person decides. Apple is the
	// one: an iCloud calendar is connected separately over CalDAV with an
	// Apple ID and an app-specific password, and signing in with Apple
	// connects nothing.
	IdentityOnly bool
}

// Info describes this provider for the UI.
func (p *Provider) Info() ProviderInfo {
	return ProviderInfo{
		ID:           p.id,
		Name:         ProviderDisplayName[p.id],
		IdentityOnly: p.id == storage.IdentityProviderApple,
	}
}

// NewProvider builds the oidc.Provider for one sign-in provider, with
// the identity scopes this package fixes. id must be one of the three
// providers storage.IdentityProvider defines.
func NewProvider(id storage.IdentityProvider, cfg ProviderConfig) (*Provider, error) {
	scopes, ok := identityScopes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, id)
	}

	issuer := cfg.Issuer
	if issuer == "" {
		issuer = DefaultIssuer[id]
	}

	provider, err := oidc.New(oidc.Config{
		Issuer:       issuer,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       scopes,
		HTTPClient:   cfg.HTTPClient,
		Now:          cfg.Now,
	})
	if err != nil {
		// oidc.New's own error names every field that is wrong and is
		// read by whoever deployed this, not by a browser. It never
		// carries the client secret's value.
		return nil, fmt.Errorf("auth: configure %s sign-in: %w", id, err)
	}

	return &Provider{id: id, provider: provider, scopes: scopes}, nil
}

// SignIn resolves a verified provider identity to a calbridge account,
// creating one on first use.
//
// The lookup is on (provider, subject) and on nothing else. It never
// consults a calendar connection, and it cannot: Store exposes no
// connection repository at all, which is what makes the rule in the
// package doc structural. A provider account somebody else connected as
// a calendar is therefore invisible here, and a sign-in whose subject
// happens to name it lands in the "no identity yet" branch below and
// gets its own, separate account, which is exactly the required
// outcome.
//
// On first use the account is created with the identity row in the same
// transaction: a users row with no password and no TOTP secret (an
// account that signs in only through its provider has neither), and an
// identities row binding the subject to it. Both are scoped to the new
// user's id, so the one write that happens before any user exists is
// still covered by that user's own policy; see db/migrations.
func SignIn(ctx context.Context, store Store, provider storage.IdentityProvider, verified oidc.Identity) (Identity, error) {
	subject := strings.TrimSpace(verified.Subject)
	if subject == "" {
		// govue verifies the ID token before this runs and an ID token
		// without a subject does not verify, so this is a guard against a
		// future caller rather than against a provider.
		return Identity{}, errors.New("auth: the verified identity carries no subject")
	}

	identity, err := store.Identities().FindByProviderSubject(ctx, provider, subject)

	switch {
	case err == nil:
		return signInExisting(ctx, store, identity.UserID)
	case errors.Is(err, storage.ErrNotFound):
		return signUpWithProvider(ctx, store, provider, subject, signInEmail(provider, subject, verified.Email))
	default:
		return Identity{}, fmt.Errorf("auth: resolve %s sign-in: %w", provider, err)
	}
}

// signInExisting loads the account an identity already points at and
// refuses a disabled one.
func signInExisting(ctx context.Context, store Store, userID uuid.UUID) (Identity, error) {
	var user storage.User

	if err := store.WithUser(ctx, userID, func(ctx context.Context) error {
		found, err := store.Users().FindByID(ctx, userID)
		if err != nil {
			return err
		}

		user = found

		return nil
	}); err != nil {
		return Identity{}, fmt.Errorf("auth: load the account behind this sign-in: %w", err)
	}

	if user.Status != storage.UserStatusActive {
		return Identity{}, ErrAccountDisabled
	}

	return identityFromUser(user), nil
}

// signUpWithProvider creates the account a first-time provider sign-in
// gets, together with the identity row binding the subject to it.
//
// The two writes do not share a transaction: Users().Create opens and
// scopes its own, because the user id it needs to scope by is the one it
// is creating. So a crash between them leaves an account with no
// identity, which is an account nobody can sign in to and nobody else
// can claim, and the next attempt with the same subject sees the email
// already taken rather than silently adopting that row. That is the safe
// side of the failure to be on, and the alternative, widening
// UserRepository so an identity could be written inside signup's own
// transaction, would put a second table inside the one transaction that
// exists to be narrow.
func signUpWithProvider(
	ctx context.Context,
	store Store,
	provider storage.IdentityProvider,
	subject, email string,
) (Identity, error) {
	user, err := store.Users().Create(ctx, storage.User{
		Email:  email,
		Status: storage.UserStatusActive,
	})
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return Identity{}, ErrEmailBelongsToAnotherAccount
		}

		return Identity{}, fmt.Errorf("auth: create an account for this %s sign-in: %w", provider, err)
	}

	if err := store.WithUser(ctx, user.ID, func(ctx context.Context) error {
		_, err := store.Identities().Create(ctx, storage.Identity{
			UserID:   user.ID,
			Provider: provider,
			Subject:  subject,
		})

		return err
	}); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			// Another request claimed this subject between the lookup
			// above and this write. The account just created is orphaned;
			// see this function's own doc comment.
			return Identity{}, ErrEmailBelongsToAnotherAccount
		}

		return Identity{}, fmt.Errorf("auth: record this %s identity: %w", provider, err)
	}

	return identityFromUser(user), nil
}

// signInEmail is the address a first-time provider sign-in creates its
// account under: the verified email claim where there is one, and a
// reserved placeholder where there is none.
//
// Apple is the provider with none, because a sign-in that stays on the
// GET callback cannot ask for the email scope (see identityScopes), and
// users.email is NOT NULL and non-empty by its own constraint. The
// placeholder is derived from the subject, so it is stable across that
// account's logins and unique to it, and it sits under the .invalid top
// level domain RFC 2606 section 2 reserves precisely so that an address
// can be guaranteed never to resolve: nothing can ever be sent to it by
// accident. Only the hash of the subject goes into it, never the subject
// itself, so an address that does end up displayed does not also publish
// the provider's identifier for that account.
func signInEmail(provider storage.IdentityProvider, subject, claimed string) string {
	if claimed = strings.TrimSpace(claimed); claimed != "" {
		return claimed
	}

	sum := sha256.Sum256([]byte(string(provider) + "\x00" + subject))

	return fmt.Sprintf("%s-%s@sign-in.calbridge.invalid", provider, hex.EncodeToString(sum[:8]))
}

// ProviderHandlerConfig is what NewProviderHandlers needs beyond the
// provider itself.
type ProviderHandlerConfig struct {
	// Logins keeps the login in flight between the redirect to the
	// provider and the callback that comes back. internal/httpapi builds
	// one session.Codec and passes the same one for every provider.
	Logins *session.Codec

	// Sessions is the manager a finished sign-in starts a session with,
	// the same one the local path's caller uses.
	Sessions *session.Manager

	// Store is where the sign-in is resolved; see SignIn.
	Store Store

	// Success is where the browser goes once the sign-in is done. Zero is
	// "/".
	Success string

	// OnError replaces what a refused sign-in is answered with. Zero is
	// govue's own plain-text answer.
	OnError func(w http.ResponseWriter, r *http.Request, err error)
}

// NewProviderHandlers wires govue's oidc.NewHandlers so that a verified
// callback resolves to an Identity through SignIn and starts a session
// with sessions.Start(ctx, w, identity.Session()), the exact call a
// successful local login also ends at.
//
// NoNext is set: govue's own next parameter would let a query string
// pick where a finished sign-in lands, and calbridge's SPA decides that
// itself. A refusal from SignIn reaches the browser as govue's opaque
// oidc.ErrProvider and never as itself, which is what that package
// documents StartSession's error to mean; the cause is in the log.
func NewProviderHandlers(provider *Provider, cfg ProviderHandlerConfig) (*oidc.Handlers, error) {
	handlers, err := oidc.NewHandlers(oidc.HandlerConfig{
		Provider: provider.provider,
		Logins:   cfg.Logins,
		StartSession: func(ctx context.Context, w http.ResponseWriter, _ *http.Request, verified oidc.Identity) error {
			identity, err := SignIn(ctx, cfg.Store, provider.id, verified)
			if err != nil {
				return err
			}

			if _, err := cfg.Sessions.Start(ctx, w, identity.Session()); err != nil {
				return fmt.Errorf("auth: start session for %s sign-in: %w", provider.id, err)
			}

			return nil
		},
		Success: cfg.Success,
		NoNext:  true,
		OnError: cfg.OnError,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: wire %s sign-in handlers: %w", provider.id, err)
	}

	return handlers, nil
}
