// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/api"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// fakeIssuer serves just enough of an OpenID Connect discovery document
// for govue's oidc package to build an authorization URL: the issuer
// itself and the three endpoints it checks. It listens on loopback,
// which govue's own endpoint check allows over plain HTTP for exactly
// this case, so no test here needs the real Google, Microsoft or Apple.
//
// Nothing below ever finishes a login against it. These tests are about
// the routes this package mounts and the parameters they send, not about
// the token exchange, which govue's own tests already cover.
func fakeIssuer(t *testing.T) string {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 server.URL,
			"authorization_endpoint": server.URL + "/authorize",
			"token_endpoint":         server.URL + "/token",
			"jwks_uri":               server.URL + "/jwks",
		})
	})

	return server.URL
}

// testProvider builds one sign-in provider against the fake issuer.
func testProvider(t *testing.T, id storage.IdentityProvider, issuer string) *auth.Provider {
	t.Helper()

	provider, err := auth.NewProvider(id, auth.ProviderConfig{
		Issuer:       issuer,
		ClientID:     "a-client-id",
		ClientSecret: "a-client-secret",
		RedirectURL:  "https://calbridge.example.com" + httpapi.ProviderCallbackPath(string(id)),
	})
	if err != nil {
		t.Fatalf("auth.NewProvider(%s) error = %v", id, err)
	}

	return provider
}

// TestProvidersListsOnlyWhatIsConfigured: a deployment that configured
// no provider offers none, and one that configured some offers exactly
// those. The login screen builds its buttons from this.
func TestProvidersListsOnlyWhatIsConfigured(t *testing.T) {
	t.Parallel()

	t.Run("none configured", func(t *testing.T) {
		t.Parallel()

		client, baseURL := testClient(t, buildTestHandlers(t, nil))

		if got := len(readProviders(t, client, baseURL).Providers); got != 0 {
			t.Fatalf("providers = %d, want 0 for a deployment offering local accounts only", got)
		}
	})

	t.Run("google and apple configured", func(t *testing.T) {
		t.Parallel()

		issuer := fakeIssuer(t)

		deps := testDeps(t)
		deps.AuthProviders = []*auth.Provider{
			testProvider(t, storage.IdentityProviderGoogle, issuer),
			testProvider(t, storage.IdentityProviderApple, issuer),
		}

		client, baseURL := testClient(t, build(t, deps))

		got := readProviders(t, client, baseURL)
		if len(got.Providers) != 2 {
			t.Fatalf("providers = %d, want 2", len(got.Providers))
		}

		byID := map[string]bool{}
		for _, provider := range got.Providers {
			byID[provider.ID] = provider.IdentityOnly

			if provider.Name == "" {
				t.Errorf("provider %q has no name for its button", provider.ID)
			}
		}

		if identityOnly, ok := byID["apple"]; !ok || !identityOnly {
			t.Error("apple is missing or not flagged identity_only; the login screen's note is built from this flag")
		}

		if identityOnly, ok := byID["google"]; !ok || identityOnly {
			t.Error("google is missing or wrongly flagged identity_only")
		}

		if _, ok := byID["microsoft"]; ok {
			t.Error("microsoft is offered although it was not configured")
		}
	})
}

// TestProviderLoginRedirectsAskingForIdentityScopesOnly is the consent
// split at the wire: the authorization URL this application actually
// sends a browser to carries the identity scopes and no calendar scope.
func TestProviderLoginRedirectsAskingForIdentityScopesOnly(t *testing.T) {
	t.Parallel()

	issuer := fakeIssuer(t)

	deps := testDeps(t)
	deps.AuthProviders = []*auth.Provider{
		testProvider(t, storage.IdentityProviderGoogle, issuer),
		testProvider(t, storage.IdentityProviderApple, issuer),
	}

	server := httptest.NewServer(build(t, deps).Public)
	t.Cleanup(server.Close)

	// No redirect is followed: the Location header is what this test is
	// about, and following it would reach the fake issuer's own 404.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	for _, id := range []string{"google", "apple"} {
		t.Run(id, func(t *testing.T) {
			resp := do(t, client, http.MethodGet, server.URL+httpapi.ProviderLoginPath(id), "", "")

			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
			}

			target, err := url.Parse(resp.Header.Get("Location"))
			if err != nil {
				t.Fatalf("parse the redirect target: %v", err)
			}

			scopes := strings.Fields(target.Query().Get("scope"))
			if len(scopes) == 0 {
				t.Fatal("the authorization request carries no scope at all")
			}

			for _, scope := range scopes {
				if strings.Contains(strings.ToLower(scope), "calendar") {
					t.Fatalf("scope = %v, want no calendar scope: signing in and connecting a calendar "+
						"are two separate consent steps", scopes)
				}
			}

			// PKCE is govue's own doing, asserted here because losing it
			// would be silent.
			if target.Query().Get("code_challenge") == "" {
				t.Error("the authorization request carries no code_challenge")
			}
		})
	}
}

// TestAnUnconfiguredProviderHasNoRoutes: nothing is mounted for a
// provider a deployment did not configure, so a request for one falls
// through to the API's own 404 rather than reaching a half-built
// handler.
func TestAnUnconfiguredProviderHasNoRoutes(t *testing.T) {
	t.Parallel()

	client, baseURL := testClient(t, buildTestHandlers(t, nil))

	for _, path := range []string{
		httpapi.ProviderLoginPath("google"),
		httpapi.ProviderCallbackPath("google"),
		httpapi.ProviderLoginPath("dropbox"),
	} {
		resp := do(t, client, http.MethodGet, baseURL+path, "", "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", path, resp.StatusCode, http.StatusNotFound)
		}
	}
}

// TestDeleteAccountRemovesTheAccountAndEndsTheSession covers the whole
// route: it needs a session, it needs the CSRF token that session
// derives, it takes the account away, and the cookie that reached it
// stops working.
func TestDeleteAccountRemovesTheAccountAndEndsTheSession(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	store := testStore(t, deps)
	body := seedAccount(t, store)

	client, baseURL := testClient(t, build(t, deps))

	if resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", body, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	meResp := do(t, client, http.MethodGet, baseURL+"/api/v1/me", "", "")

	var me meResponse
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /api/v1/me body: %v", err)
	}

	// Without the session's own CSRF token the deletion is refused by the
	// protector, like every other unsafe route.
	if resp := do(t, client, http.MethodDelete, baseURL+"/api/v1/me", "", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("delete without a csrf token status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	if store.CountUsers() != 1 {
		t.Fatal("the refused deletion removed the account anyway")
	}

	if resp := do(t, client, http.MethodDelete, baseURL+"/api/v1/me", "", me.CSRFToken); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if got := store.CountUsers(); got != 0 {
		t.Fatalf("users = %d, want 0 after the account was deleted", got)
	}

	if resp := do(t, client, http.MethodGet, baseURL+"/api/v1/me", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me status after deletion = %d, want %d: the cookie must stop working",
			resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestDeleteAccountNeedsASession(t *testing.T) {
	t.Parallel()

	client, baseURL := testClient(t, buildTestHandlers(t, nil))

	resp := do(t, client, http.MethodDelete, baseURL+"/api/v1/me", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d with no session", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestSignUpRefusesAnAddressThatAlreadyHasAnAccount: the second signup
// answers 409, which is the one signup refusal that says something about
// an existing account and cannot be avoided, since the address is unique
// across calbridge.
func TestSignUpRefusesAnAddressThatAlreadyHasAnAccount(t *testing.T) {
	t.Parallel()

	client, baseURL := testClient(t, buildTestHandlers(t, nil))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, testEmail, testPassword)

	if resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/signup", body, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("first signup status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/signup", body, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second signup status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

// TestAShedRequestIsAnswered503AndNeverAsAFailedLogin: internal/auth
// caps how many Argon2id derivations run at once, and a request over
// that cap never had its password checked. Answering 401 would tell
// somebody with the right password that it is wrong; 503 says what is
// actually true, that this server is at capacity.
func TestAShedRequestIsAnswered503AndNeverAsAFailedLogin(t *testing.T) {
	t.Parallel()

	tests := map[string]func(error) error{
		"login":  httpapi.LoginError,
		"signup": httpapi.SignUpError,
	}

	for name, mapError := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			answer := api.AsError(mapError(fmt.Errorf("wrapped: %w", auth.ErrHashingBusy)))

			if answer.Status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", answer.Status, http.StatusServiceUnavailable)
			}

			if answer.Status == http.StatusUnauthorized {
				t.Fatal("a shed request was answered as a failed login")
			}
		})
	}
}

// TestALoginRefusalStaysOne401: every wrong credential still answers
// alike, so adding the shed path above did not pull the refusals apart.
func TestALoginRefusalStaysOne401(t *testing.T) {
	t.Parallel()

	answer := api.AsError(httpapi.LoginError(auth.ErrInvalidCredentials))

	if answer.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", answer.Status, http.StatusUnauthorized)
	}
}

// readProviders reads GET /api/v1/auth/providers.
func readProviders(t *testing.T, client *http.Client, baseURL string) providersResponse {
	t.Helper()

	resp := do(t, client, http.MethodGet, baseURL+"/api/v1/auth/providers", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("providers status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var providers providersResponse
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		t.Fatalf("decode the providers body: %v", err)
	}

	return providers
}
