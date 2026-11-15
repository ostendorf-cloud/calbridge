// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/calbridge/internal/landingapi"
)

// testAppURL is the configured value every test below expects the served
// shell to carry, in place of landingapi's own placeholder token.
const testAppURL = "https://app.calbridge.example"

// testIndexHTML carries the placeholder a real build's index.html
// carries, so a test can assert the substitution actually ran rather
// than merely that some HTML came back.
const testIndexHTML = `<!doctype html><meta name="calbridge-app-url" content="__CALBRIDGE_APP_URL__">`

func testDistFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte(testIndexHTML)},
		"assets/app.js": {Data: []byte("// test asset")},
	}
}

func testDeps(t *testing.T) landingapi.Deps {
	t.Helper()

	return landingapi.Deps{
		Logger:          logging.New(io.Discard, slog.LevelError),
		HealthRegistry:  health.New(),
		MetricsRegistry: metrics.NewRegistry(),
		DistFS:          testDistFS(),
		AppURL:          testAppURL,
	}
}

func TestNewHandlerRejectsAnEmptyDeps(t *testing.T) {
	t.Parallel()

	_, err := landingapi.NewHandler(landingapi.Deps{})
	if err == nil {
		t.Fatal("NewHandler accepted an empty Deps")
	}
	for _, want := range []string{"logger", "health registry", "metrics registry", "dist filesystem", "app URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestPublicServesTheIndexShellWithTheConfiguredAppURL(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "__CALBRIDGE_APP_URL__") {
		t.Error("the placeholder is still present; the configured app URL was not substituted")
	}
	if !strings.Contains(rec.Body.String(), testAppURL) {
		t.Errorf("body = %q, want it to carry the configured app URL %q", rec.Body.String(), testAppURL)
	}
}

func TestPublicFallsBackToTheIndexShellForAnUnknownRoute(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/pricing", nil)
	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d for the client-side router's own route", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), testAppURL) {
		t.Errorf("body = %q, want the substituted index shell", rec.Body.String())
	}
}

func TestPublicServesARealAssetUnsubstituted(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/app.js", nil)
	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "// test asset" {
		t.Errorf("body = %q, want the real asset untouched", rec.Body.String())
	}
}

func TestAdminServesLivenessReadinessAndMetrics(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		handlers.Admin.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s on Admin = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}

func TestPublicNeverServesLivenessReadinessOrMetrics(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	// Public has no apiPathPrefix carve-out, so these three paths are
	// not registered on it at all and fall through to the SPA shell,
	// exactly like any other unmatched path. Asserting that is the
	// point: a caller reaching this application through its Ingress or
	// HTTPRoute must never see a probe or a metrics answer there, only
	// the static shell every other unmatched path gets.
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		handlers.Public.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s on Public = %d, want %d (the SPA shell)", path, rec.Code, http.StatusOK)
		}
		if !strings.Contains(rec.Body.String(), testAppURL) {
			t.Errorf("GET %s on Public did not answer with the SPA shell", path)
		}
	}
}
