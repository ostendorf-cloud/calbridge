// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// errorCode reads the code out of an API error answer.
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode the error body: %v", err)
	}

	return body.Error.Code
}

// TestSignUpOnAClosedInstanceIsRefusedAndExistingAccountsStillLogIn is the
// switch over HTTP: the endpoint answers 403 with its own code, no account
// is created, and an account that already exists signs in as before.
func TestSignUpOnAClosedInstanceIsRefusedAndExistingAccountsStillLogIn(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	store := testStore(t, deps)
	login := seedAccount(t, store)

	deps.RegistrationClosed = true

	client, baseURL := testClient(t, build(t, deps))

	signUp := fmt.Sprintf(`{"email":%q,"password":%q}`, "newcomer@example.com", testPassword)

	resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/signup", signUp, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("signup status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	if got := errorCode(t, resp); got != api.CodePermissionDenied {
		t.Fatalf("signup error code = %q, want %q", got, api.CodePermissionDenied)
	}

	if got := store.CountUsers(); got != 1 {
		t.Fatalf("users = %d, want 1: the refused signup created an account", got)
	}

	if resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", login, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d, want %d for an existing account", resp.StatusCode, http.StatusNoContent)
	}
}

// TestProvidersReportsWhetherRegistrationIsOpen: the login screen builds
// its signup switch from this flag.
func TestProvidersReportsWhetherRegistrationIsOpen(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		closed bool
		want   bool
	}{
		"nothing configured": {closed: false, want: true},
		"closed":             {closed: true, want: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t)
			deps.RegistrationClosed = test.closed

			client, baseURL := testClient(t, build(t, deps))

			if got := readProviders(t, client, baseURL).RegistrationOpen; got != test.want {
				t.Fatalf("registration_open = %v, want %v", got, test.want)
			}
		})
	}
}

// TestAClosedRegistrationIsAnswered403OnEveryPath: both account creating
// paths map the one typed refusal to the same answer, and that answer is
// neither a retry hint nor a conflict.
func TestAClosedRegistrationIsAnswered403OnEveryPath(t *testing.T) {
	t.Parallel()

	tests := map[string]func(error) error{
		"signup":           httpapi.SignUpError,
		"provider sign-in": httpapi.ProviderSignInError,
	}

	for name, mapError := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			answer := api.AsError(mapError(fmt.Errorf("wrapped: %w", auth.ErrRegistrationClosed)))

			if answer.Status != http.StatusForbidden || answer.Code != api.CodePermissionDenied {
				t.Fatalf("answer = %d %q, want %d %q",
					answer.Status, answer.Code, http.StatusForbidden, api.CodePermissionDenied)
			}
		})
	}
}

// closedFlowKey is generated once per run: an RSA key costs real time.
var closedFlowKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	return key
})

// completeIssuer is fakeIssuer extended to finish a sign-in: it also
// serves a key set and a token endpoint, and the token it hands back is
// whatever setToken last stored. It listens on loopback, which govue's
// endpoint check allows over plain HTTP.
func completeIssuer(t *testing.T) (issuer string, setToken func(string)) {
	t.Helper()

	var (
		mu    sync.Mutex
		token string
	)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	writeJSON := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode the fake issuer's answer: %v", err)
		}
	}

	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 server.URL,
			"authorization_endpoint": server.URL + "/authorize",
			"token_endpoint":         server.URL + "/token",
			"jwks_uri":               server.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		public := closedFlowKey().PublicKey

		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kid": "key-1",
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		writeJSON(w, map[string]string{"id_token": token, "access_token": "unused", "token_type": "Bearer"})
	})

	return server.URL, func(value string) {
		mu.Lock()
		defer mu.Unlock()

		token = value
	}
}

// signIDToken signs an ID token for the issuer, valid now.
func signIDToken(t *testing.T, issuer, clientID, subject, email, nonce string) string {
	t.Helper()

	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode a token part: %v", err)
		}

		return base64.RawURLEncoding.EncodeToString(raw)
	}

	now := time.Now()
	signing := encode(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "key-1"}) + "." + encode(map[string]any{
		"iss":            issuer,
		"sub":            subject,
		"aud":            clientID,
		"exp":            now.Add(5 * time.Minute).Unix(),
		"iat":            now.Unix(),
		"nonce":          nonce,
		"email":          email,
		"email_verified": true,
	})

	digest := sha256.Sum256([]byte(signing))

	signature, err := rsa.SignPKCS1v15(rand.Reader, closedFlowKey(), crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign the id token: %v", err)
	}

	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// TestAProviderCallbackOnAClosedInstanceIsAnswered403AndCreatesNoAccount
// runs a whole provider sign-in through the mounted routes. It holds the
// wiring down: the switch reaches the provider handlers, and the refusal
// reaches the browser as itself instead of as the opaque sign-in failure.
func TestAProviderCallbackOnAClosedInstanceIsAnswered403AndCreatesNoAccount(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		closed     bool
		wantStatus int
		wantUsers  int
	}{
		"closed": {closed: true, wantStatus: http.StatusForbidden, wantUsers: 0},
		"open":   {closed: false, wantStatus: http.StatusSeeOther, wantUsers: 1},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			issuer, setToken := completeIssuer(t)

			provider, err := auth.NewProvider(storage.IdentityProviderGoogle, auth.ProviderConfig{
				Issuer:       issuer,
				ClientID:     "a-client-id",
				ClientSecret: "a-client-secret",
				RedirectURL:  "https://calbridge.example.com" + httpapi.ProviderCallbackPath("google"),
			})
			if err != nil {
				t.Fatalf("auth.NewProvider() error = %v", err)
			}

			deps := testDeps(t)
			deps.AllowPlainHTTP = true
			deps.RegistrationClosed = test.closed
			deps.AuthProviders = []*auth.Provider{provider}

			server := httptest.NewServer(build(t, deps).Public)
			t.Cleanup(server.Close)

			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatalf("cookiejar.New() error = %v", err)
			}

			client := &http.Client{
				Jar:           jar,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}

			start := do(t, client, http.MethodGet, server.URL+httpapi.ProviderLoginPath("google"), "", "")
			if start.StatusCode != http.StatusSeeOther {
				t.Fatalf("login status = %d, want %d", start.StatusCode, http.StatusSeeOther)
			}

			target, err := url.Parse(start.Header.Get("Location"))
			if err != nil {
				t.Fatalf("parse the redirect to the provider: %v", err)
			}

			setToken(signIDToken(t, issuer, "a-client-id", "google-subject-1", "person@example.com", target.Query().Get("nonce")))

			callback := httpapi.ProviderCallbackPath("google") + "?" +
				url.Values{"state": {target.Query().Get("state")}, "code": {"an-authorization-code"}}.Encode()

			resp := do(t, client, http.MethodGet, server.URL+callback, "", "")
			if resp.StatusCode != test.wantStatus {
				t.Fatalf("callback status = %d, want %d", resp.StatusCode, test.wantStatus)
			}

			if test.closed {
				if got := errorCode(t, resp); got != api.CodePermissionDenied {
					t.Fatalf("callback error code = %q, want %q", got, api.CodePermissionDenied)
				}
			}

			if got := testStore(t, deps).CountUsers(); got != test.wantUsers {
				t.Fatalf("users = %d, want %d", got, test.wantUsers)
			}
		})
	}
}
