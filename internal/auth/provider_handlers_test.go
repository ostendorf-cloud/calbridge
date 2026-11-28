// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

const (
	flowIssuer   = "https://accounts.example.test"
	flowClientID = "calbridge-client"
	flowKeyID    = "key-1"
)

// flowKey is generated once per run, since an RSA key costs real time and
// every test here needs the same one.
var flowKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	return key
})

// fakeIdentityProvider answers the three endpoints a sign-in reads, in
// process: the discovery document, the key set and the token endpoint. The
// token endpoint hands back whatever idToken holds.
type fakeIdentityProvider struct {
	t       *testing.T
	mu      sync.Mutex
	idToken string
}

func (p *fakeIdentityProvider) setIDToken(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.idToken = token
}

func (p *fakeIdentityProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body any

	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		body = map[string]any{
			"issuer":                 flowIssuer,
			"authorization_endpoint": flowIssuer + "/authorize",
			"token_endpoint":         flowIssuer + "/token",
			"jwks_uri":               flowIssuer + "/jwks",
		}
	case "/jwks":
		public := flowKey().PublicKey
		body = map[string]any{"keys": []map[string]string{{
			"kid": flowKeyID,
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}}}
	case "/token":
		p.mu.Lock()
		token := p.idToken
		p.mu.Unlock()

		body = map[string]string{"id_token": token, "access_token": "unused", "token_type": "Bearer"}
	default:
		http.NotFound(w, r)

		return
	}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		p.t.Errorf("encode the fake provider's answer: %v", err)
	}
}

// RoundTrip routes a request into the fake with no socket involved.
func (p *fakeIdentityProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, r)

	resp := rec.Result()
	resp.Request = r

	return resp, nil
}

// signToken builds a signed ID token for subject, bound to nonce.
func signToken(t *testing.T, subject, email, nonce string) string {
	t.Helper()

	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode a token part: %v", err)
		}

		return base64.RawURLEncoding.EncodeToString(raw)
	}

	signing := encode(map[string]string{"alg": "RS256", "typ": "JWT", "kid": flowKeyID}) + "." + encode(map[string]any{
		"iss":            flowIssuer,
		"sub":            subject,
		"aud":            flowClientID,
		"exp":            testNow.Add(5 * time.Minute).Unix(),
		"iat":            testNow.Unix(),
		"nonce":          nonce,
		"email":          email,
		"email_verified": true,
	})

	digest := sha256.Sum256([]byte(signing))

	signature, err := rsa.SignPKCS1v15(rand.Reader, flowKey(), crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign the id token: %v", err)
	}

	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// signInFlow is the real provider handler pair against the fake provider.
type signInFlow struct {
	t        *testing.T
	store    *storagetest.Store
	provider *fakeIdentityProvider
	handlers *auth.ProviderHandlers
	codec    *session.Codec

	// refused is what OnError was handed, nil when it never ran.
	refused error
}

func newSignInFlow(t *testing.T, store *storagetest.Store, options ...auth.Option) *signInFlow {
	t.Helper()

	flow := &signInFlow{t: t, store: store, provider: &fakeIdentityProvider{t: t}}

	provider, err := auth.NewProvider(storage.IdentityProviderGoogle, auth.ProviderConfig{
		Issuer:       flowIssuer,
		ClientID:     flowClientID,
		ClientSecret: "a-client-secret",
		RedirectURL:  "https://calbridge.example.test/api/v1/auth/google/callback",
		HTTPClient:   &http.Client{Transport: flow.provider},
		Now:          fixedNow,
	})
	if err != nil {
		t.Fatalf("auth.NewProvider() error = %v", err)
	}

	flow.codec, err = session.NewCodec(session.CodecConfig{
		Key:            []byte("a login signing key of at least thirty two bytes"),
		AllowPlainHTTP: true,
		Now:            fixedNow,
	})
	if err != nil {
		t.Fatalf("session.NewCodec() error = %v", err)
	}

	sessions, err := session.New(session.Config{
		Store:          session.NewMemoryStore(nil, 10),
		AllowPlainHTTP: true,
	})
	if err != nil {
		t.Fatalf("session.New() error = %v", err)
	}

	flow.handlers, err = auth.NewProviderHandlers(provider, auth.ProviderHandlerConfig{
		Logins:   flow.codec,
		Sessions: sessions,
		Store:    store,
		Options:  options,
		OnError: func(w http.ResponseWriter, _ *http.Request, err error) {
			flow.refused = err

			w.WriteHeader(http.StatusForbidden)
		},
	})
	if err != nil {
		t.Fatalf("auth.NewProviderHandlers() error = %v", err)
	}

	return flow
}

func (f *signInFlow) serve(handler http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()

	ctx := logging.NewContext(f.t.Context(), logging.New(io.Discard, slog.LevelDebug))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)

	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// signIn runs a whole sign-in for a subject: the redirect to the provider,
// then the callback the provider sends the browser back to.
func (f *signInFlow) signIn(subject, email string) *httptest.ResponseRecorder {
	f.t.Helper()

	start := f.serve(f.handlers.Login(), "/api/v1/auth/google/login")
	if start.Code != http.StatusSeeOther {
		f.t.Fatalf("login status = %d, want %d", start.Code, http.StatusSeeOther)
	}

	redirect, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		f.t.Fatalf("parse the redirect to the provider: %v", err)
	}

	var cookie *http.Cookie

	for _, candidate := range start.Result().Cookies() {
		if candidate.Name == f.codec.LoginCookieName() {
			cookie = candidate
		}
	}

	if cookie == nil {
		f.t.Fatal("the login set no login cookie")
	}

	f.provider.setIDToken(signToken(f.t, subject, email, redirect.Query().Get("nonce")))

	query := url.Values{"state": {redirect.Query().Get("state")}, "code": {"an-authorization-code"}}

	return f.serve(f.handlers.Callback(), "/api/v1/auth/google/callback?"+query.Encode(), cookie)
}

// TestProviderCallbackOnAClosedInstanceHandsTheRefusalToOnError is the whole
// provider path end to end. govue turns every StartSession error into its own
// opaque error, so a refusal reaching OnError as itself is what lets the
// browser be told why.
func TestProviderCallbackOnAClosedInstanceHandsTheRefusalToOnError(t *testing.T) {
	t.Parallel()

	store := storagetest.NewStore()
	flow := newSignInFlow(t, store, auth.WithRegistrationClosed())

	rec := flow.signIn("google-subject-1", "person@example.com")

	if !errors.Is(flow.refused, auth.ErrRegistrationClosed) {
		t.Fatalf("OnError got %v, want ErrRegistrationClosed", flow.refused)
	}

	if rec.Code != http.StatusForbidden {
		t.Fatalf("callback status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	if got := store.CountUsers(); got != 0 {
		t.Fatalf("users = %d, want 0: a closed instance must not register through a provider", got)
	}
}

// TestProviderCallbackCreatesTheAccountWhenTheInstanceIsOpen keeps the test
// above honest: it proves the fake provider gets a sign-in all the way to
// the account, so the refusal there is the switch and nothing else.
func TestProviderCallbackCreatesTheAccountWhenTheInstanceIsOpen(t *testing.T) {
	t.Parallel()

	store := storagetest.NewStore()
	flow := newSignInFlow(t, store)

	rec := flow.signIn("google-subject-1", "person@example.com")

	if flow.refused != nil {
		t.Fatalf("OnError got %v, want no refusal on an open instance", flow.refused)
	}

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	if got := store.CountUsers(); got != 1 {
		t.Fatalf("users = %d, want 1", got)
	}
}

// TestProviderCallbackStillSignsInALinkedAccountOnAClosedInstance covers the
// existing account that keeps working while registration is closed.
func TestProviderCallbackStillSignsInALinkedAccountOnAClosedInstance(t *testing.T) {
	t.Parallel()

	store := storagetest.NewStore()

	identity := signedInAs(t, "google-subject-1", "person@example.com", true)
	if _, err := auth.SignIn(t.Context(), store, storage.IdentityProviderGoogle, identity); err != nil {
		t.Fatalf("SignIn() on the open instance error = %v", err)
	}

	flow := newSignInFlow(t, store, auth.WithRegistrationClosed())

	rec := flow.signIn("google-subject-1", "person@example.com")

	if flow.refused != nil {
		t.Fatalf("OnError got %v, want no refusal for a linked account", flow.refused)
	}

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	if got := store.CountUsers(); got != 1 {
		t.Fatalf("users = %d, want 1", got)
	}
}
