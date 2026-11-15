// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// generousRateLimit is a burst high enough that no test below the rate
// limiting tests themselves ever trips it, the same reasoning solobill's
// own httpapi_test.go uses for its default Deps.
const generousRateLimit = 1000

// indexHTML is the SPA shell's own content, distinct from any API error
// body, so a test can tell the fallback and a JSON answer apart. It
// carries the public site URL placeholder the committed web/index.html
// carries, because newSPAHandler refuses a build without one.
const indexHTML = `<!doctype html><title>calbridge test shell</title>` +
	`<meta name="calbridge-public-site-url" content="__CALBRIDGE_PUBLIC_SITE_URL__">`

// servedIndexHTML is indexHTML as the handler serves it under testDeps's
// own empty PublicSiteURL: the placeholder substituted for nothing,
// which is what web/'s own composable reads as "render no link back".
const servedIndexHTML = `<!doctype html><title>calbridge test shell</title>` +
	`<meta name="calbridge-public-site-url" content="">`

// testDistFS is a minimal embedded-build stand-in: an index.html and one
// real asset, matching what vite's own build always produces.
func testDistFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte(indexHTML)},
		"assets/app.js": {Data: []byte("// test asset")},
	}
}

// testNow is the fixed clock every handler built here runs its TOTP
// check against, so a step boundary can never make a test flaky.
var testNow = time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

// testEmail and testPassword are the account seedAccount creates. The
// password is obviously not a credential for anything and is only long
// enough to satisfy auth.MinPasswordLength.
const (
	testEmail    = "person@example.com"
	testPassword = "a-long-enough-test-passphrase"
)

// testDeps is the Deps every handler in this file is built from, with
// its own empty store. A test that needs to reach that store takes it
// from the returned value.
func testDeps(t *testing.T) httpapi.Deps {
	t.Helper()

	return httpapi.Deps{
		Logger:          logging.New(io.Discard, slog.LevelError),
		HealthRegistry:  health.New(),
		MetricsRegistry: metrics.NewRegistry(),
		DistFS:          testDistFS(),
		RateLimitBurst:  generousRateLimit,
		RateLimitPer:    time.Minute,
		// The grant route has a bucket of its own, and a test about
		// anything else must not be able to spend it either.
		GrantRateLimitBurst: generousRateLimit,
		GrantRateLimitPer:   time.Minute,
		Store:               storagetest.NewStore(),
		Now:                 func() time.Time { return testNow },
	}
}

// buildTestHandlers builds a real pair of handlers over a fresh
// health.Registry and metrics registry, so a test exercises the actual
// middleware chains and routing, with no network.
func buildTestHandlers(t *testing.T, registry *health.Registry) httpapi.Handlers {
	t.Helper()

	deps := testDeps(t)
	if registry != nil {
		deps.HealthRegistry = registry
	}

	return build(t, deps)
}

// build is the one place a test turns Deps into handlers, so a
// construction failure reads the same wherever it happens.
func build(t *testing.T, deps httpapi.Deps) httpapi.Handlers {
	t.Helper()

	handlers, err := httpapi.NewHandler(deps)
	if err != nil {
		t.Fatalf("httpapi.NewHandler() error = %v", err)
	}

	return handlers
}

// testStore is the in-memory store testDeps put on deps, for a test
// that needs to seed or inspect it.
func testStore(t *testing.T, deps httpapi.Deps) *storagetest.Store {
	t.Helper()

	store, ok := deps.Store.(*storagetest.Store)
	if !ok {
		t.Fatalf("deps.Store is a %T, want the in-memory store", deps.Store)
	}

	return store
}

// seedAccount creates the local account testEmail names, directly
// through internal/auth rather than over HTTP, and returns a login body
// carrying a code valid at testNow. The body is good for one login: a
// second use of the same code is a replay, which internal/auth refuses
// on purpose.
func seedAccount(t *testing.T, store *storagetest.Store) string {
	t.Helper()

	local := auth.NewLocal(store, func() time.Time { return testNow })

	_, enrolment, err := local.SignUp(t.Context(), testEmail, testPassword)
	if err != nil {
		t.Fatalf("seed the account: %v", err)
	}

	code, err := auth.GenerateTOTPCode(enrolment.Secret, testNow)
	if err != nil {
		t.Fatalf("generate the totp code: %v", err)
	}

	return fmt.Sprintf(`{"email":%q,"password":%q,"totp_code":%q}`, testEmail, testPassword, code)
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	t.Run("missing logger is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{
			HealthRegistry:  health.New(),
			MetricsRegistry: metrics.NewRegistry(),
			DistFS:          testDistFS(),
		})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing logger")
		}
	})

	t.Run("missing health registry is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{
			Logger:          logging.New(io.Discard, slog.LevelError),
			MetricsRegistry: metrics.NewRegistry(),
			DistFS:          testDistFS(),
		})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing health registry")
		}
	})

	t.Run("missing metrics registry is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{
			Logger:         logging.New(io.Discard, slog.LevelError),
			HealthRegistry: health.New(),
			DistFS:         testDistFS(),
		})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing metrics registry")
		}
	})

	t.Run("missing dist filesystem is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{
			Logger:          logging.New(io.Discard, slog.LevelError),
			HealthRegistry:  health.New(),
			MetricsRegistry: metrics.NewRegistry(),
		})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing dist filesystem")
		}
	})

	t.Run("missing store is an error", func(t *testing.T) {
		t.Parallel()

		deps := testDeps(t)
		deps.Store = nil

		_, err := httpapi.NewHandler(deps)
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing store")
		}
	})

	t.Run("every problem is reported together", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for every missing dependency")
		}
		if !errorContainsAll(err, "logger is required", "health registry is required",
			"metrics registry is required", "dist filesystem is required", "store is required") {
			t.Errorf("NewHandler() error = %v, want it to name every missing dependency", err)
		}
	})
}

func TestAdminHealthAndMetricsEndpoints(t *testing.T) {
	t.Parallel()

	registry := health.New()
	handlers := buildTestHandlers(t, registry)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "liveness", path: "/healthz", wantStatus: http.StatusOK},
		{name: "readiness with no checks registered", path: "/readyz", wantStatus: http.StatusOK},
		{name: "metrics", path: "/metrics", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handlers.Admin.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// TestAdminHealthAndMetricsEndpointsAreUnreachableOnPublic covers the two
// decisions httpapi.go's own comment on the admin-path 404 registrations
// argues for: registering with no method, so every method gets the 404
// and not only GET, and registering both the exact path and its
// trailing-slash subtree, so a path under it cannot fall through to the
// SPA catch-all either.
func TestAdminHealthAndMetricsEndpointsAreUnreachableOnPublic(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "liveness", method: http.MethodGet, path: "/healthz"},
		{name: "readiness", method: http.MethodGet, path: "/readyz"},
		{name: "metrics", method: http.MethodGet, path: "/metrics"},
		{name: "liveness trailing slash subtree", method: http.MethodGet, path: "/healthz/"},
		{name: "metrics subtree path", method: http.MethodGet, path: "/metrics/foo"},
		{name: "liveness with a non-GET method", method: http.MethodPost, path: "/healthz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			handlers.Public.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d: liveness, readiness and metrics must not be reachable on Public", rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestReadinessFailsOnceDraining(t *testing.T) {
	t.Parallel()

	registry := health.New()
	handlers := buildTestHandlers(t, registry)

	registry.Drain()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handlers.Admin.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d once draining", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessFailsOnAFailingCheck(t *testing.T) {
	t.Parallel()

	registry := health.New()
	registry.Add("fixture", 0, func(context.Context) error { return errors.New("unreachable") })
	handlers := buildTestHandlers(t, registry)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handlers.Admin.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d for a failing check", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSecurityHeadersAreSetOnEveryPublicResponse(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()

	handlers.Public.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}

func TestRequestIDHeaderIsSetOnEveryResponse(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	for name, handler := range map[string]http.Handler{"public": handlers.Public, "admin": handlers.Admin} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/does-not-exist", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if got := rec.Header().Get("X-Request-Id"); got == "" {
				t.Error("X-Request-Id header is empty, want a generated request id")
			}
		})
	}
}

func TestUnmatchedAPIRouteAnswersJSON404(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/does-not-exist", nil)
	rec := httptest.NewRecorder()

	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for an unmatched API route", rec.Code, http.StatusNotFound)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json for an unmatched API route", ct)
	}
}

func TestUnmatchedNonAPIRouteServesTheSPAShell(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	for _, path := range []string{"/", "/does-not-exist", "/edges/123"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			rec := httptest.NewRecorder()

			handlers.Public.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for the SPA fallback", rec.Code, http.StatusOK)
			}
			if rec.Body.String() != servedIndexHTML {
				t.Errorf("body = %q, want the embedded index.html", rec.Body.String())
			}
		})
	}
}

func TestSPAFallbackServesARealAsset(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()

	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d for a real asset", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "// test asset" {
		t.Errorf("body = %q, want the asset's own content", rec.Body.String())
	}
}

// testClient is an http.Client with a cookie jar, pointed at a server
// wrapping handlers.Public, so a test can exercise the login, me and
// logout contract the way a browser actually would: cookies carried
// across requests, never read or replayed by hand.
func testClient(t *testing.T, handlers httpapi.Handlers) (*http.Client, string) {
	t.Helper()

	server := httptest.NewServer(handlers.Public)
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}

	return &http.Client{Jar: jar}, server.URL
}

// do sends method/url/body through client and closes the response body
// once t ends, so every call site reads the response without repeating
// the request construction or the cleanup.
func do(t *testing.T, client *http.Client, method, url, body, csrfToken string) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	if err != nil {
		t.Fatalf("build %s %s request: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})

	return resp
}

type meResponse struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	CSRFToken string `json:"csrf_token"`
}

type signUpResponse struct {
	TOTPSecret string `json:"totp_secret"`
	TOTPURI    string `json:"totp_uri"`
}

type providersResponse struct {
	Providers []struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		IdentityOnly     bool   `json:"identity_only"`
		PlaceholderEmail bool   `json:"placeholder_email"`
	} `json:"providers"`
}

func TestMeWithoutASessionIsUnauthenticated(t *testing.T) {
	t.Parallel()

	client, baseURL := testClient(t, buildTestHandlers(t, nil))

	resp := do(t, client, http.MethodGet, baseURL+"/api/v1/me", "", "")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d with no session", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestLoginRefusalsAllLookTheSame: whichever part of the attempt was
// wrong, the answer is the same 401. internal/auth collapses them on
// purpose and this route must not pull them apart again.
func TestLoginRefusalsAllLookTheSame(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	validBody := seedAccount(t, testStore(t, deps))

	client, baseURL := testClient(t, build(t, deps))

	tests := map[string]string{
		"no account for this address": `{"email":"nobody@example.com","password":"a-long-enough-test-passphrase","totp_code":"123456"}`,
		"wrong password":              fmt.Sprintf(`{"email":%q,"password":"another-long-passphrase","totp_code":"123456"}`, testEmail),
		"wrong code":                  fmt.Sprintf(`{"email":%q,"password":%q,"totp_code":"000000"}`, testEmail, testPassword),
		"nothing at all":              `{}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", body, "")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}

	// The valid body still works afterwards, so the refusals above were
	// refusals and not a handler that rejects everything.
	resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", validBody, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want %d for the correct credentials", resp.StatusCode, http.StatusNoContent)
	}
}

// TestSignUpThenLoginThenMeThenLogout is the whole local path over HTTP:
// signup hands back the enrolment, the code it produces logs in, /me
// answers the session and its csrf token, and logout spends that token.
func TestSignUpThenLoginThenMeThenLogout(t *testing.T) {
	t.Parallel()

	client, baseURL := testClient(t, buildTestHandlers(t, nil))

	signUpResp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/signup",
		fmt.Sprintf(`{"email":%q,"password":%q}`, testEmail, testPassword), "")
	if signUpResp.StatusCode != http.StatusOK {
		t.Fatalf("signup status = %d, want %d", signUpResp.StatusCode, http.StatusOK)
	}

	var enrolment signUpResponse
	if err := json.NewDecoder(signUpResp.Body).Decode(&enrolment); err != nil {
		t.Fatalf("decode the signup body: %v", err)
	}

	if enrolment.TOTPSecret == "" || !strings.HasPrefix(enrolment.TOTPURI, "otpauth://totp/") {
		t.Fatalf("signup answered %+v, want a totp secret and an otpauth uri", enrolment)
	}

	// Signup starts no session: the person has to prove the enrolment
	// worked by logging in with a code from it.
	if _, ok := sessionCookie(signUpResp); ok {
		t.Error("signup set a session cookie, want none until the second factor is used")
	}

	code, err := auth.GenerateTOTPCode(enrolment.TOTPSecret, testNow)
	if err != nil {
		t.Fatalf("generate the totp code: %v", err)
	}

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q,"totp_code":%q}`, testEmail, testPassword, code)

	loginResp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", loginBody, "")
	if loginResp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d, want %d", loginResp.StatusCode, http.StatusNoContent)
	}

	meResp := do(t, client, http.MethodGet, baseURL+"/api/v1/me", "", "")
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want %d once logged in", meResp.StatusCode, http.StatusOK)
	}

	var me meResponse
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decode /api/v1/me body: %v", err)
	}
	if me.Email != testEmail {
		t.Errorf("me.Email = %q, want %q", me.Email, testEmail)
	}
	if _, err := uuid.Parse(me.UserID); err != nil {
		t.Errorf("me.UserID = %q, want the users row id the session is keyed on", me.UserID)
	}
	if me.CSRFToken == "" {
		t.Error("me.CSRFToken is empty, want the session's own csrf token")
	}

	// A logout without the CSRF token the session actually holds is
	// refused: the protector must guard this route like every other
	// unsafe one.
	forbidden := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/logout", "", "")
	if forbidden.StatusCode != http.StatusForbidden {
		t.Errorf("logout without a csrf token status = %d, want %d", forbidden.StatusCode, http.StatusForbidden)
	}

	logoutResp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/logout", "", me.CSRFToken)
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Errorf("logout status = %d, want %d", logoutResp.StatusCode, http.StatusNoContent)
	}

	afterLogout := do(t, client, http.MethodGet, baseURL+"/api/v1/me", "", "")
	if afterLogout.StatusCode != http.StatusUnauthorized {
		t.Errorf("me status after logout = %d, want %d", afterLogout.StatusCode, http.StatusUnauthorized)
	}
}

// wrongCredentials is a login body no account matches, for a test that
// is about the middleware in front of the handler rather than about the
// handler's own answer.
const wrongCredentials = `{"email":"nobody@example.com","password":"a-long-enough-test-passphrase","totp_code":"000000"}`

// TestSignupAndLoginAreRateLimited proves both unauthenticated routes
// carry the rate limiter, which is what maxSessions's own doc comment
// relies on. The limiter runs ahead of the handler, so a refused
// credential still spends a token: the attempts below answer 401 until
// the burst is gone and 429 afterwards, and it is that change that shows
// the limiter is there. Each call uses a fresh client with no cookie
// jar, so every request arrives with no session of its own and the CSRF
// protector has nothing to check; they still share one bucket, since
// clientip.Resolver keys by the loopback address all of them connect
// from.
func TestSignupAndLoginAreRateLimited(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path, body     string
		beforeTheLimit int
	}{
		"login": {"/api/v1/auth/login", wrongCredentials, http.StatusUnauthorized},
		// A password shorter than auth.MinPasswordLength is refused by
		// the handler, so this spends a token without creating an
		// account each time.
		"signup": {"/api/v1/auth/signup", `{"email":"someone@example.com","password":"short"}`, http.StatusBadRequest},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t)
			deps.RateLimitBurst = 2

			server := httptest.NewServer(build(t, deps).Public)
			t.Cleanup(server.Close)

			attempt := func() int {
				return do(t, &http.Client{}, http.MethodPost, server.URL+test.path, test.body, "").StatusCode
			}

			for i := range 2 {
				if status := attempt(); status != test.beforeTheLimit {
					t.Fatalf("attempt %d status = %d, want %d", i, status, test.beforeTheLimit)
				}
			}

			if status := attempt(); status != http.StatusTooManyRequests {
				t.Errorf("attempt after exhausting the burst status = %d, want %d",
					status, http.StatusTooManyRequests)
			}
		})
	}
}

// TestLoginRateLimitKeyHonoursTrustedProxyOnly proves clientip.Resolver's
// own contract reaches the login rate limiter correctly: with no trusted
// proxy configured, a forged X-Forwarded-For is ignored and the limiter
// keys on the TCP peer alone, matching config.Config.TrustedProxyCIDRs's
// own doc comment. Configuring the test server's loopback peer as trusted
// makes the resolver honour that header instead, so two requests that
// share the same TCP peer but carry different client addresses in it get
// their own separate budget.
func TestLoginRateLimitKeyHonoursTrustedProxyOnly(t *testing.T) {
	t.Parallel()

	loginAs := func(t *testing.T, client *http.Client, baseURL, forwardedFor string) int {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/v1/auth/login", strings.NewReader(wrongCredentials))
		if err != nil {
			t.Fatalf("build login request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })

		return resp.StatusCode
	}

	// newClient gives the caller a client whose Transport is its own, never
	// the implicit http.DefaultTransport a zero-value http.Client falls
	// back to. httptest.Server.Close closes http.DefaultTransport's idle
	// connections as a courtesy to callers that use it, and both subtests
	// below run in parallel, each against its own server. Sharing that
	// default transport would let one subtest's server teardown race the
	// still-open keep-alive connection another subtest's own sequence of
	// requests depends on.
	newClient := func(t *testing.T) *http.Client {
		t.Helper()

		client := &http.Client{Transport: &http.Transport{}}
		t.Cleanup(client.CloseIdleConnections)

		return client
	}

	t.Run("no trusted proxy configured ignores a forged header", func(t *testing.T) {
		t.Parallel()

		deps := testDeps(t)
		deps.RateLimitBurst = 1

		server := httptest.NewServer(build(t, deps).Public)
		t.Cleanup(server.Close)
		client := newClient(t)

		// The credentials are wrong on purpose: this test is about which
		// key the limiter counts against, not about the handler's answer,
		// and the limiter runs ahead of the handler either way.
		if status := loginAs(t, client, server.URL, "203.0.113.1"); status != http.StatusUnauthorized {
			t.Fatalf("first login status = %d, want %d", status, http.StatusUnauthorized)
		}

		// A different forged client address buys no second bucket: with
		// no trusted proxy, clientip.Resolver never reads
		// X-Forwarded-For at all and keys this request on the same TCP
		// peer as the one above.
		if status := loginAs(t, client, server.URL, "203.0.113.2"); status != http.StatusTooManyRequests {
			t.Errorf("second login with a different forged address status = %d, want %d: the rate limiter must key on the TCP peer, not on an untrusted header", status, http.StatusTooManyRequests)
		}
	})

	t.Run("a configured trusted proxy honours the header", func(t *testing.T) {
		t.Parallel()

		loopback, err := clientip.ParseCIDRs("127.0.0.1/32")
		if err != nil {
			t.Fatalf("clientip.ParseCIDRs() error = %v", err)
		}

		deps := testDeps(t)
		deps.RateLimitBurst = 1
		deps.TrustedProxyCIDRs = loopback

		server := httptest.NewServer(build(t, deps).Public)
		t.Cleanup(server.Close)
		client := newClient(t)

		// httptest.NewServer listens on 127.0.0.1, so every request below
		// shares that same TCP peer; trusting that peer is what makes the
		// resolver read X-Forwarded-For and separate them by the address
		// it names instead.
		if status := loginAs(t, client, server.URL, "203.0.113.1"); status != http.StatusUnauthorized {
			t.Fatalf("first client login status = %d, want %d", status, http.StatusUnauthorized)
		}
		if status := loginAs(t, client, server.URL, "203.0.113.2"); status != http.StatusUnauthorized {
			t.Errorf("second client login status = %d, want %d: a trusted proxy must give each forwarded address its own budget", status, http.StatusUnauthorized)
		}

		// Reusing the first client's own address exhausts its budget
		// again, proving the separation above came from the resolved
		// client address the trusted proxy's header named, not from some
		// other cause.
		if status := loginAs(t, client, server.URL, "203.0.113.1"); status != http.StatusTooManyRequests {
			t.Errorf("repeated first client login status = %d, want %d: the trusted proxy's header must still be honoured on this request", status, http.StatusTooManyRequests)
		}
	})
}

// sessionCookie returns the session cookie resp carries, or false when it
// carries none: a helper so the two tests below do not each repeat the
// name matching, since the cookie's own name changes with AllowPlainHTTP.
func sessionCookie(resp *http.Response) (*http.Cookie, bool) {
	for _, c := range resp.Cookies() {
		if strings.HasSuffix(c.Name, "session") {
			return c, true
		}
	}
	return nil, false
}

// TestSessionCookieIsSecureByDefault proves AllowPlainHTTP's default is
// the secure one: Secure set and the __Host- prefix kept, which is what
// config.Config.SessionAllowPlainHTTP's own doc comment promises a
// deployment that never sets SESSION_ALLOW_PLAIN_HTTP.
func TestSessionCookieIsSecureByDefault(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	body := seedAccount(t, testStore(t, deps))

	client, baseURL := testClient(t, build(t, deps))

	resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", body, "")

	cookie, ok := sessionCookie(resp)
	if !ok {
		t.Fatal("login set no session cookie")
	}
	if !cookie.Secure {
		t.Errorf("cookie %q Secure = %v, want true by default", cookie.Name, cookie.Secure)
	}
	if !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Errorf("cookie name = %q, want the __Host- prefix by default", cookie.Name)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got == "" {
		t.Error("Strict-Transport-Security header is empty, want it set by default")
	}
}

// TestSessionCookieDropsSecureWhenPlainHTTPIsAllowed proves the opt-in
// side of the same decision: AllowPlainHTTP true drops Secure, the
// __Host- prefix it is a precondition of, and HSTS, so a local plain
// HTTP run still gets a working session cookie instead of one a browser
// refuses to store at all.
func TestSessionCookieDropsSecureWhenPlainHTTPIsAllowed(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.AllowPlainHTTP = true
	body := seedAccount(t, testStore(t, deps))

	client, baseURL := testClient(t, build(t, deps))

	resp := do(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", body, "")

	cookie, ok := sessionCookie(resp)
	if !ok {
		t.Fatal("login set no session cookie")
	}
	if cookie.Secure {
		t.Errorf("cookie %q Secure = %v, want false when plain HTTP is allowed", cookie.Name, cookie.Secure)
	}
	if strings.HasPrefix(cookie.Name, "__Host-") {
		t.Errorf("cookie name = %q, want the __Host- prefix dropped when plain HTTP is allowed", cookie.Name)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security header = %q, want it omitted when plain HTTP is allowed", got)
	}
}

// indexHTMLWithPublicSiteURLPlaceholder is a minimal embedded build
// carrying nothing but the placeholder web/index.html carries, so
// TestPublicSiteURLIsRenderedIntoTheSPAShell can assert the served body
// exactly, without testDistFS's own title in the way.
const indexHTMLWithPublicSiteURLPlaceholder = `<!doctype html><meta name="calbridge-public-site-url" content="__CALBRIDGE_PUBLIC_SITE_URL__">`

// committedIndexHTMLPath is web/'s own index.html as committed, the file
// vite copies into the build output and the one the substitution has to
// find its placeholder in.
const committedIndexHTMLPath = "../../web/index.html"

// TestNewHandlerFailsOnAnIndexWithNoPlaceholder is the refusal that
// makes the placeholder a contract rather than a convention: dropping
// the meta tag from web/index.html would otherwise remove the link back
// to the public site from every deployment, silently and with every
// check still green.
func TestNewHandlerFailsOnAnIndexWithNoPlaceholder(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.DistFS = fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>no placeholder here</title>")},
	}

	_, err := httpapi.NewHandler(deps)
	if err == nil {
		t.Fatal("NewHandler accepted an index.html with no public site URL placeholder")
	}
	if !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("error %q does not mention the missing placeholder", err)
	}
}

// TestTheCommittedIndexCarriesThePlaceholder reads web/index.html
// itself. Every other test here builds its markup in Go, so all of them
// keep passing if the meta tag is dropped from the committed file: the
// refusal above would then fire in production and nowhere else. This is
// the one test that ties the two together.
func TestTheCommittedIndexCarriesThePlaceholder(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(committedIndexHTMLPath)
	if err != nil {
		t.Fatalf("read the committed index.html: %v", err)
	}

	if !strings.Contains(string(committed), "__CALBRIDGE_PUBLIC_SITE_URL__") {
		t.Errorf(
			"%s carries no __CALBRIDGE_PUBLIC_SITE_URL__ placeholder; newSPAHandler refuses such a build at startup",
			committedIndexHTMLPath,
		)
	}
}

// TestTheCommittedIndexIsSubstitutedAsMarkup serves the committed
// index.html itself, so the substitution is exercised against the real
// file and not only against a hand-written approximation of it.
func TestTheCommittedIndexIsSubstitutedAsMarkup(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(committedIndexHTMLPath)
	if err != nil {
		t.Fatalf("read the committed index.html: %v", err)
	}

	deps := testDeps(t)
	deps.DistFS = fstest.MapFS{"index.html": {Data: committed}}
	deps.PublicSiteURL = "https://calbridge.example.com"
	handlers := build(t, deps)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handlers.Public.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "__CALBRIDGE_PUBLIC_SITE_URL__") {
		t.Error("the placeholder is still present in the served committed shell")
	}
	if !strings.Contains(body, `content="https://calbridge.example.com"`) {
		t.Errorf("body = %q, want the public site URL inside the meta tag's content attribute", body)
	}
}

// TestAPublicSiteURLWithMarkupInItIsEscaped is about the substitution
// being an escape and not a paste. net/url accepts a double quote, an
// angle bracket and an ampersand inside a host and url.URL.String
// re-emits each of them verbatim, so internal/config's own
// checkPublicSiteURL lets every value below through. Unescaped, the
// first of them ends the content attribute early and the rest of the tag
// is reparsed as attribute names; the served document has to stay
// well-formed for whatever a deployment configures.
func TestAPublicSiteURLWithMarkupInItIsEscaped(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		publicSiteURL string
		want          string
	}{
		"a double quote ends the attribute": {
			publicSiteURL: `https://x"`,
			want:          `<!doctype html><meta name="calbridge-public-site-url" content="https://x&#34;">`,
		},
		"an angle bracket opens a tag": {
			publicSiteURL: `https://x"><script>alert(1)</script>`,
			want: `<!doctype html><meta name="calbridge-public-site-url" ` +
				`content="https://x&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;">`,
		},
		"an ampersand starts an entity": {
			publicSiteURL: `https://x&y`,
			want:          `<!doctype html><meta name="calbridge-public-site-url" content="https://x&amp;y">`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t)
			deps.DistFS = fstest.MapFS{
				"index.html": {Data: []byte(indexHTMLWithPublicSiteURLPlaceholder)},
			}
			deps.PublicSiteURL = tc.publicSiteURL
			handlers := build(t, deps)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handlers.Public.ServeHTTP(rec, req)

			if rec.Body.String() != tc.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestPublicSiteURLIsRenderedIntoTheSPAShell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		publicSiteURL string
		want          string
	}{
		{
			name:          "configured",
			publicSiteURL: "https://calbridge.example.com",
			want:          `<!doctype html><meta name="calbridge-public-site-url" content="https://calbridge.example.com">`,
		},
		{
			name:          "a different host renders too, proving the value is not hardcoded",
			publicSiteURL: "https://landing.staging.example.org",
			want:          `<!doctype html><meta name="calbridge-public-site-url" content="https://landing.staging.example.org">`,
		},
		{
			name:          "left unset",
			publicSiteURL: "",
			want:          `<!doctype html><meta name="calbridge-public-site-url" content="">`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t)
			deps.DistFS = fstest.MapFS{
				"index.html": {Data: []byte(indexHTMLWithPublicSiteURLPlaceholder)},
			}
			deps.PublicSiteURL = tc.publicSiteURL
			handlers := build(t, deps)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handlers.Public.ServeHTTP(rec, req)

			if rec.Body.String() != tc.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// errorContainsAll reports whether err's message contains every one of
// subs, used only to assert that NewHandler joins every construction
// problem into one error rather than reporting the first and discarding
// the rest.
func errorContainsAll(err error, subs ...string) bool {
	msg := err.Error()
	for _, sub := range subs {
		if !strings.Contains(msg, sub) {
			return false
		}
	}
	return true
}
