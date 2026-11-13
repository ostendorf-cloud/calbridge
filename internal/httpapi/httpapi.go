// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package httpapi builds the HTTP handlers calbridge answers requests
// with: liveness, readiness and metrics today, with the calendar, edge,
// grant and webhook routes a later issue adds to the public one.
//
// Every dependency is constructed by cmd/calbridge's main and handed to
// NewHandler through Deps; this package builds no logger, no health
// registry and no metrics registry of its own.
//
// # Two handlers, two listeners
//
// NewHandler builds two handlers, not one: Public, which cmd/calbridge
// runs through govue's httpserver.Config on the listener its Ingress or
// HTTPRoute routes to, and Admin, which it runs through
// httpserver.AdminConfig on a second listener reached only from inside
// the cluster. Liveness, readiness and metrics live on Admin and nowhere
// on Public: anything that can reach this application through its
// Ingress or HTTPRoute could otherwise scrape its metrics or poll its
// probes, which is the vulnerability this split closes. See govue's own
// example/app.go (adminHandler) and httpserver.RunAdmin's own doc
// comment for the shape this reproduces, and cmd/calbridge's main for
// how the two listeners are run and drained together.
//
// # The middleware chain order
//
// Public gets one outer chain wrapping its mux, outermost first:
// Recover, so a panic anywhere in the chain is turned into a logged line
// and an opaque 500 instead of a crashed goroutine; RequestID, so every
// later middleware and handler can read the same request id back off the
// context; the security headers, so even an error response carries them;
// the HTTP metrics, which must sit directly on the mux to read the
// matched route pattern off the request once routing has happened; and
// the access log, innermost, for the same reason. This is the order
// govue's own example application documents (see govue's example/app.go)
// and that this package reproduces exactly; changing it changes what a
// log line or a metric label can observe.
//
// Public mounts no route of its own yet: this milestone adds no CORS, no
// rate limiting and no CSRF protection, and no business route either. A
// later issue's webhook endpoint belongs here, on Public, never on
// Admin.
//
// Admin gets a deliberately short chain instead, matching govue's own
// adminHandler: Recover and RequestID only. A probe or a scrape is not
// browser traffic, so the security headers have nothing to protect, and
// a scraper polling this listener every few seconds must not fill the
// access log the way a public request would. The HTTP metrics
// middleware is absent too, deliberately: instrumenting a scrape of the
// metrics endpoint itself would feed Prometheus a metric about scraping
// Prometheus, which is not a loop worth closing.
package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
)

// Deps is everything NewHandler needs, constructed by cmd/calbridge's
// main.
type Deps struct {
	Logger *slog.Logger

	// HealthRegistry already carries every readiness check main wants;
	// this package only mounts it, on Admin, never on Public.
	HealthRegistry *health.Registry

	// MetricsRegistry collects every metric calbridge records: NewHandler
	// registers the HTTP request metrics into it, and Admin mounts its
	// scrape endpoint. A later package that adds a metric of its own
	// registers directly into this same registry, the one main
	// constructs through govue's metrics.NewRegistry.
	MetricsRegistry *prometheus.Registry
}

// Handlers is what NewHandler builds: Public, served on the listener the
// application's Ingress or HTTPRoute routes to, and Admin, served on
// govue's second, administrative listener. See the package doc's "Two
// handlers, two listeners" section for why these are never the same
// handler.
type Handlers struct {
	Public http.Handler
	Admin  http.Handler
}

// NewHandler builds both handlers, returning every construction problem
// at once rather than stopping at the first one, the same pattern
// config.Load uses.
func NewHandler(d Deps) (Handlers, error) {
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
	if d.MetricsRegistry == nil {
		fail(errors.New("httpapi: metrics registry is required"))
	}

	if err := errors.Join(problems...); err != nil {
		return Handlers{}, fmt.Errorf("httpapi: build handler: %w", err)
	}

	security := middleware.DefaultSecurity()
	accessLog := middleware.AccessLog{}
	httpMetrics := metrics.NewHTTP(d.MetricsRegistry)
	metricsHandler := metrics.Handler(d.MetricsRegistry)

	mux := http.NewServeMux()

	// The outer chain, outermost first: see the package doc for why each
	// position is forced. RequestID and the security middleware both
	// call (*http.Request).WithContext, which both httpMetrics and
	// accessLog need to not have happened yet, since they read
	// r.Pattern off the exact request value http.ServeMux matched
	// against.
	public := middleware.Chain(mux,
		middleware.Recover(d.Logger),
		middleware.RequestID(d.Logger),
		security.Middleware,
		httpMetrics.Middleware,
		accessLog.Middleware,
	)

	// adminMux carries liveness, readiness and metrics, and nothing
	// else: see the package doc's "Two handlers, two listeners" section
	// for why these never reach Public. metricsHandler reads from the
	// same MetricsRegistry httpMetrics.Middleware above records into, so
	// a scrape here reports the traffic Public actually served.
	adminMux := http.NewServeMux()
	d.HealthRegistry.Handle(adminMux)
	adminMux.Handle("GET /metrics", metricsHandler)

	// The admin chain, deliberately short: see the package doc's "The
	// middleware chain order" section for why only Recover and
	// RequestID belong here.
	admin := middleware.Chain(adminMux,
		middleware.Recover(d.Logger),
		middleware.RequestID(d.Logger),
	)

	return Handlers{Public: public, Admin: admin}, nil
}
