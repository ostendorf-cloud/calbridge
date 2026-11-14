// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

	// PlaceholderEmail marks a provider that never hands over an address
	// this application will believe, so an account created through it is
	// known by a placeholder instead. The UI says so before somebody
	// picks that provider rather than after, because nothing in this
	// release can change the address afterwards; see signInEmail for
	// which providers these are and why.
	PlaceholderEmail bool
}

// neverSendsAVerifiedEmail lists the providers whose accounts always end
// up on a placeholder address. Microsoft emits no email_verified claim
// at all and Apple is never asked for the email scope; see signInEmail
// for both. It is a fact about each provider rather than about a
// deployment, so it is fixed here and not configurable.
var neverSendsAVerifiedEmail = map[storage.IdentityProvider]bool{
	storage.IdentityProviderMicrosoft: true,
	storage.IdentityProviderApple:     true,
}

// Info describes this provider for the UI.
func (p *Provider) Info() ProviderInfo {
	return ProviderInfo{
		ID:               p.id,
		Name:             ProviderDisplayName[p.id],
		IdentityOnly:     p.id == storage.IdentityProviderApple,
		PlaceholderEmail: neverSendsAVerifiedEmail[p.id],
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
		return signUpWithProvider(ctx, store, provider, subject, signInEmail(provider, subject, verified))
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
// is creating. A failure of the second write therefore has to undo the
// first here, and does: without that, the account left behind holds the
// address for good, and because an Apple account's address is derived
// from its subject (see signInEmail), that same person's every later
// Apple sign-in would resolve to the same address and be refused
// forever, with nothing in this application able to release it. A
// cancelled request is enough to reach that path, since a closed tab
// during the callback cancels the context, which is why the undo runs on
// a context of its own.
//
// What the undo cannot cover is the process dying between the two
// writes. That leaves exactly the orphan described above, and clearing
// one is an operator's job until account recovery exists.
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
		undoProviderSignUp(ctx, store, user.ID)

		if errors.Is(err, storage.ErrConflict) {
			// Another request claimed this subject between the lookup
			// above and this write, and now owns it.
			return Identity{}, ErrEmailBelongsToAnotherAccount
		}

		return Identity{}, fmt.Errorf("auth: record this %s identity: %w", provider, err)
	}

	return identityFromUser(user), nil
}

// undoProviderSignUp removes the account signUpWithProvider created when
// the identity that was to be bound to it could not be written, so the
// address it holds goes back to being free.
//
// It runs on a context detached from the request's cancellation, with a
// deadline of its own: the commonest way to reach this path is a request
// that was cancelled, and a cleanup inheriting that cancellation would
// be refused before it started, leaving exactly the row it exists to
// remove.
//
// A failure of the cleanup itself is deliberately not returned. The
// caller is already answering a failed sign-in, and the orphan is the
// smaller of the two problems to report; it is logged by nothing here
// because this package holds no logger, which is a gap worth closing
// when one is threaded through it.
func undoProviderSignUp(ctx context.Context, store Store, userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), undoTimeout)
	defer cancel()

	_ = store.Users().DeleteAccount(ctx, userID)
}

// undoTimeout bounds undoProviderSignUp's own work. It is short on
// purpose: the account being removed was created seconds ago and owns
// nothing else yet, so the delete touches one row, and a database that
// cannot manage that in this long is not going to manage it at all.
const undoTimeout = 5 * time.Second

// PlaceholderEmailDomain is the domain signInEmail puts an account under
// when the provider gave it no address it is willing to believe. It is a
// subdomain of the .invalid top level domain RFC 2606 section 2 reserves
// precisely so that a name can be guaranteed never to resolve, so
// nothing can ever be sent to one of these by accident.
//
// It is this application's own namespace, which is why Local.SignUp
// refuses an address in it: a local signup able to pick one could
// otherwise claim the slot a particular provider account is going to
// land in, before that person ever arrives.
const PlaceholderEmailDomain = "sign-in.calbridge.invalid"

// signInEmail is the address a first-time provider sign-in creates its
// account under: the provider's email claim when that claim is both
// present and marked verified, and a placeholder under
// PlaceholderEmailDomain otherwise.
//
// Requiring email_verified, rather than taking the address the token
// states, is what keeps a provider's claim from reserving somebody
// else's address here. The linking rule already stops this from being a
// takeover, since a first-time sign-in never adopts an existing account,
// but an unverified claim could still create an account displaying a
// stranger's address and block that person's own signup, which is reason
// enough not to believe one.
//
// Which branch each configured provider takes, in practice:
//
//   - Google sends email_verified and takes the claim.
//   - Microsoft always takes the placeholder. Entra ID emits no
//     email_verified claim at all, in either token version; its own ID
//     token claims reference says of email that the value "isn't
//     guaranteed to be correct and is mutable over time" and to "never
//     use it for authorization or to save data for a user". This is not
//     an unusual tenant, it is every Microsoft account, and it is
//     deliberate: the alternative is believing an address a tenant
//     administrator controls.
//   - Apple always takes the placeholder, because it is never asked for
//     the email scope at all (see identityScopes).
//
// So two of the three configured providers land every account on a
// placeholder address, and that is the address GET /api/v1/me reports
// and the UI shows. There is no way to set or change an account's
// address in this package, so there is no recovery from it either. An
// address the person can confirm and correct is the real answer and is
// its own issue; until it exists, charts/calbridge/README.md tells an
// operator enabling Microsoft sign-in what to expect, and the login
// view says so where somebody chooses a provider.
//
// The placeholder is derived from the subject, so it is stable across
// that account's sign-ins and unique to it. Only a hash of the subject
// goes into it, never the subject itself, so an address that does end up
// displayed does not also publish the provider's identifier for that
// account.
func signInEmail(provider storage.IdentityProvider, subject string, verified oidc.Identity) string {
	if claimed := strings.TrimSpace(verified.Email); claimed != "" && emailClaimIsVerified(verified.IDToken) {
		return claimed
	}

	sum := sha256.Sum256([]byte(string(provider) + "\x00" + subject))

	return fmt.Sprintf("%s-%s@%s", provider, hex.EncodeToString(sum[:8]), PlaceholderEmailDomain)
}

// emailClaimIsVerified reports whether an ID token says its email claim
// has been verified by the provider.
//
// The claim is read out of the token's payload here because govue's oidc
// package has no field for it: oidc.Identity carries the address but not
// its verification. Decoding the payload a second time is safe and is
// not a second opinion on the token: this runs only on the token
// Provider.Exchange already verified the signature, issuer, audience,
// expiry and nonce of, and nothing here is trusted beyond this one
// boolean.
//
// Every failure answers false. A token that does not parse, a claim that
// is absent, a claim of some other type: none of them is evidence that
// an address belongs to whoever is signing in, and the only cost of
// answering false is a placeholder address.
//
// Both spellings are accepted, the boolean the OpenID Connect core
// specification defines and the string "true" some providers send in its
// place; the specification itself notes the claim may be a boolean or a
// string carrying one.
func emailClaimIsVerified(idToken string) bool {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	var claims struct {
		EmailVerified any `json:"email_verified"`
	}

	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}

	switch value := claims.EmailVerified.(type) {
	case bool:
		return value
	case string:
		return value == "true"
	default:
		return false
	}
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
