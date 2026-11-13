// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
)

// buildTestHandlers builds a real pair of handlers over a fresh
// health.Registry and metrics registry, so a test exercises the actual
// middleware chains and routing, with no network.
func buildTestHandlers(t *testing.T, registry *health.Registry) httpapi.Handlers {
	t.Helper()

	if registry == nil {
		registry = health.New()
	}

	handlers, err := httpapi.NewHandler(httpapi.Deps{
		Logger:          logging.New(io.Discard, slog.LevelError),
		HealthRegistry:  registry,
		MetricsRegistry: metrics.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("httpapi.NewHandler() error = %v", err)
	}

	return handlers
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	t.Run("missing logger is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{
			HealthRegistry:  health.New(),
			MetricsRegistry: metrics.NewRegistry(),
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
		})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing metrics registry")
		}
	})

	t.Run("every problem is reported together", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for every missing dependency")
		}
		if !errorContainsAll(err, "logger is required", "health registry is required", "metrics registry is required") {
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

func TestAdminHealthAndMetricsEndpointsAreUnreachableOnPublic(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
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

func TestUnmatchedRouteAnswers404(t *testing.T) {
	t.Parallel()

	handlers := buildTestHandlers(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()

	handlers.Public.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for an unmatched route", rec.Code, http.StatusNotFound)
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
