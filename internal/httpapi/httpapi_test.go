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

	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
)

// buildTestHandler builds a real handler over a fresh health.Registry, so
// a test exercises the actual middleware chain and routing, with no
// network.
func buildTestHandler(t *testing.T, registry *health.Registry) http.Handler {
	t.Helper()

	if registry == nil {
		registry = health.New()
	}

	handler, err := httpapi.NewHandler(httpapi.Deps{
		Logger:         logging.New(io.Discard, slog.LevelError),
		HealthRegistry: registry,
	})
	if err != nil {
		t.Fatalf("httpapi.NewHandler() error = %v", err)
	}

	return handler
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	t.Run("missing logger is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{HealthRegistry: health.New()})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing logger")
		}
	})

	t.Run("missing health registry is an error", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{Logger: logging.New(io.Discard, slog.LevelError)})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for a missing health registry")
		}
	})

	t.Run("every problem is reported together", func(t *testing.T) {
		t.Parallel()

		_, err := httpapi.NewHandler(httpapi.Deps{})
		if err == nil {
			t.Fatal("NewHandler() error = nil, want an error for every missing dependency")
		}
		if !errorContainsBoth(err, "logger is required", "health registry is required") {
			t.Errorf("NewHandler() error = %v, want it to name both missing dependencies", err)
		}
	})
}

func TestHealthAndMetricsEndpoints(t *testing.T) {
	t.Parallel()

	registry := health.New()
	handler := buildTestHandler(t, registry)

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

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestReadinessFailsOnceDraining(t *testing.T) {
	t.Parallel()

	registry := health.New()
	handler := buildTestHandler(t, registry)

	registry.Drain()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d once draining", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessFailsOnAFailingCheck(t *testing.T) {
	t.Parallel()

	registry := health.New()
	registry.Add("fixture", 0, func(context.Context) error { return errors.New("unreachable") })
	handler := buildTestHandler(t, registry)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d for a failing check", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSecurityHeadersAreSetOnEveryResponse(t *testing.T) {
	t.Parallel()

	handler := buildTestHandler(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}

func TestRequestIDHeaderIsSetOnEveryResponse(t *testing.T) {
	t.Parallel()

	handler := buildTestHandler(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Error("X-Request-Id header is empty, want a generated request id")
	}
}

func TestUnmatchedRouteAnswers404(t *testing.T) {
	t.Parallel()

	handler := buildTestHandler(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for an unmatched route", rec.Code, http.StatusNotFound)
	}
}

// errorContainsBoth reports whether err's message contains both substrings,
// used only to assert that NewHandler joins every construction problem
// into one error rather than reporting the first and discarding the rest.
func errorContainsBoth(err error, a, b string) bool {
	msg := err.Error()
	return strings.Contains(msg, a) && strings.Contains(msg, b)
}
