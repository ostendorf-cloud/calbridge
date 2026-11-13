// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package httpapi builds the HTTP handler calbridge answers requests
// with: liveness, readiness and metrics today, with the calendar, edge,
// grant and webhook routes a later issue adds to the same mux.
//
// Every dependency is constructed by cmd/calbridge's main and handed to
// NewHandler through Deps; this package builds no logger, no health
// registry and no metrics registry of its own.
//
// # The middleware chain order
//
// One outer chain wraps the whole mux, outermost first: Recover, so a
// panic anywhere in the chain is turned into a logged line and an opaque
// 500 instead of a crashed goroutine; RequestID, so every later middleware
// and handler can read the same request id back off the context; the
// security headers, so even an error response carries them; the HTTP
// metrics, which must sit directly on the mux to read the matched route
// pattern off the request once routing has happened; and the access log,
// innermost, for the same reason. This is the order govue's own example
// application documents (see govue's example/app.go) and that this
// package reproduces exactly; changing it changes what a log line or a
// metric label can observe.
//
// Liveness, readiness and metrics are mounted on the mux directly, like
// every other route: none of them needs a per-route chain today, since
// this milestone adds no CORS, no rate limiting and no CSRF protection.
// A later issue that adds one builds a second, per-route chain the same
// way govue's example application does, rather than folding it into this
// one outer chain.
package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
)

// Deps is everything NewHandler needs, constructed by cmd/calbridge's
// main.
type Deps struct {
	Logger *slog.Logger

	// HealthRegistry already carries every readiness check main wants;
	// this package only mounts it.
	HealthRegistry *health.Registry

	// Now is the clock the access log timestamps with. Nil is time.Now;
	// tests replace it.
	Now func() time.Time
}

// NewHandler builds the complete HTTP handler, returning every
// construction problem at once rather than stopping at the first one,
// the same pattern config.Load uses.
func NewHandler(d Deps) (http.Handler, error) {
	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	if d.Logger == nil {
		fail(errors.New("httpapi: logger is required"))
	}
	if d.HealthRegistry == nil {
		fail(errors.New("httpapi: health registry is required"))
	}

	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("httpapi: build handler: %w", err)
	}

	security := middleware.DefaultSecurity()
	accessLog := middleware.AccessLog{Now: d.Now}

	metricsRegistry := metrics.NewRegistry()
	httpMetrics := metrics.NewHTTP(metricsRegistry)
	metricsHandler := metrics.Handler(metricsRegistry)

	mux := http.NewServeMux()

	d.HealthRegistry.Handle(mux)
	mux.Handle("GET /metrics", metricsHandler)

	// The outer chain, outermost first: see the package doc for why each
	// position is forced. RequestID and the security middleware both
	// call (*http.Request).WithContext, which both httpMetrics and
	// accessLog need to not have happened yet, since they read
	// r.Pattern off the exact request value http.ServeMux matched
	// against.
	return middleware.Chain(mux,
		middleware.Recover(d.Logger),
		middleware.RequestID(d.Logger),
		security.Middleware,
		httpMetrics.Middleware,
		accessLog.Middleware,
	), nil
}
