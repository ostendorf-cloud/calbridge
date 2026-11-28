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
	"github.com/ostendorf-cloud/calbridge/internal/landingconfig"
)

// testAppURL is the configured value every test below expects the served
// shell to carry, in place of landingapi's own placeholder token.
const testAppURL = "https://app.calbridge.example"

// testIndexHTML carries the placeholder a real build's index.html
// carries, so a test can assert the substitution actually ran rather
// than merely that some HTML came back.
const testIndexHTML = `<!doctype html><meta name="calbridge-app-url" content="__CALBRIDGE_APP_URL__">` +
	`<meta name="calbridge-operator" content="__CALBRIDGE_OPERATOR__">`

// testOperator is a complete operator, shaped like what the deployment
// supplies.
var testOperator = landingconfig.Operator{
	Name:       "Erika Mustermann",
	Street:     "Musterstrasse 12",
	PostalCode: "10115",
	City:       "Berlin",
	Email:      "kontakt@calbridge.example",
}

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
		Operator:        testOperator,
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

// TestPublicAnswersEveryRouterPageWith200 pins the paths the client-side
// router owns. web-landing/src/router/index.test.ts pins the same list,
// so a page added on one side only fails on the other.
func TestPublicAnswersEveryRouterPageWith200(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	for _, path := range []string{
		"/",
		"/pricing",
		"/de/imprint",
		"/de/privacy",
		"/de/terms",
		"/en/imprint",
		"/en/privacy",
		"/en/terms",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			handlers.Public.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if !strings.Contains(rec.Body.String(), testAppURL) {
				t.Errorf("body = %q, want the substituted index shell", rec.Body.String())
			}
		})
	}
}

func TestPublicAnswersAnUnknownPathWith404(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	for _, path := range []string{
		"/does-not-exist",
		"/agb/impressum",
		"/fr/privacy",
		"/en",
		"/en/",
		"/en/privacy/",
		"/en/cookies",
		"/de/en/privacy",
		"/EN/privacy",
		"/assets/missing.js",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			handlers.Public.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
			}
		})
	}
}

// TestPublicServesTheShellWithItsStatusOnANotFoundPath keeps the 404 page
// the visitor sees: the router needs the shell to render it.
func TestPublicServesTheShellWithItsStatusOnANotFoundPath(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/does-not-exist", nil)
	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), testAppURL) {
		t.Errorf("body = %q, want the substituted index shell", rec.Body.String())
	}
}

func TestPublicRedirectsAShortLegalPathToThePageOfTheVisitorsLanguage(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	cases := map[string]struct {
		path           string
		acceptLanguage string
		cookie         string
		wantLocation   string
	}{
		"privacy for a German visitor": {
			path: "/privacy", acceptLanguage: "de-DE,de;q=0.9", wantLocation: "/de/privacy",
		},
		"privacy for an English visitor": {
			path: "/privacy", acceptLanguage: "en-GB,en;q=0.9", wantLocation: "/en/privacy",
		},
		"imprint for a German visitor": {
			path: "/imprint", acceptLanguage: "de", wantLocation: "/de/imprint",
		},
		"terms for an English visitor": {
			path: "/terms", acceptLanguage: "en", wantLocation: "/en/terms",
		},
		"privacy with no header falls back to English": {
			path: "/privacy", wantLocation: "/en/privacy",
		},
		"privacy for a language the site does not serve falls back to English": {
			path: "/privacy", acceptLanguage: "fr-FR,fr;q=0.9", wantLocation: "/en/privacy",
		},
		"the stored choice beats the header": {
			path: "/privacy", acceptLanguage: "en", cookie: "de", wantLocation: "/de/privacy",
		},
		"an unknown cookie value is ignored": {
			path: "/privacy", acceptLanguage: "de", cookie: "../x", wantLocation: "/de/privacy",
		},
		"the German imprint alias": {
			path: "/impressum", acceptLanguage: "en", wantLocation: "/de/imprint",
		},
		"the German privacy alias": {
			path: "/datenschutz", acceptLanguage: "en", wantLocation: "/de/privacy",
		},
		"the long German privacy alias": {
			path: "/datenschutzerklaerung", acceptLanguage: "en", wantLocation: "/de/privacy",
		},
		"the German terms alias": {
			path: "/agb", acceptLanguage: "en", wantLocation: "/de/terms",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
			if tc.acceptLanguage != "" {
				req.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "govue-language", Value: tc.cookie})
			}
			handlers.Public.ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
			}
			if got := rec.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("Location = %q, want %q", got, tc.wantLocation)
			}
		})
	}
}

// TestPublicNeverBuildsARedirectTargetFromTheRequest covers the one thing
// the acceptance criteria forbid: a target made of user input beyond the
// language choice.
func TestPublicNeverBuildsARedirectTargetFromTheRequest(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/privacy?next=https://evil.example", nil)
	req.Host = "evil.example"
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Referer", "https://evil.example/")
	handlers.Public.ServeHTTP(rec, req)

	if got := rec.Header().Get("Location"); got != "/en/privacy" {
		t.Errorf("Location = %q, want %q", got, "/en/privacy")
	}
}

func TestPublicDoesNotRedirectAPrefixedLegalPage(t *testing.T) {
	t.Parallel()

	handlers, err := landingapi.NewHandler(testDeps(t))
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/en/privacy", nil)
	req.Header.Set("Accept-Language", "de")
	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d: a prefixed page answers in the language it names", rec.Code, http.StatusOK)
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
	// not registered on it at all and fall through to the not found
	// shell, exactly like any other unmatched path. Asserting that is
	// the point: a caller reaching this application through its Ingress
	// or HTTPRoute must never see a probe or a metrics answer there,
	// only the static shell every other unmatched path gets.
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		handlers.Public.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s on Public = %d, want %d (the not found shell)", path, rec.Code, http.StatusNotFound)
		}
		if !strings.Contains(rec.Body.String(), testAppURL) {
			t.Errorf("GET %s on Public did not answer with the SPA shell", path)
		}
	}
}
