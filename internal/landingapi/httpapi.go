// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package landingapi builds the public landing server's HTTP handlers:
// liveness, readiness and metrics on Admin, and on Public nothing but
// the embedded static build. There is no session, no CSRF protector and
// no database here at all: this build answers the same request the same
// way for every visitor, which is what lets a correction to the privacy
// policy ship without touching the application internal/httpapi serves.
//
// NewHandler builds two handlers, not one, for the same reason
// internal/httpapi does: Public, served on the listener the landing
// Deployment's Ingress or HTTPRoute routes to, and Admin, served on a
// second listener reached only from inside the cluster. See that
// package's own doc comment's "Two handlers, two listeners" section for
// the full reasoning, reproduced here at a smaller scale.
package landingapi

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/language"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"

	"github.com/ostendorf-cloud/calbridge/internal/landingconfig"
)

// Deps is everything NewHandler needs, constructed by
// cmd/calbridge-landing's main.
type Deps struct {
	Logger *slog.Logger

	// HealthRegistry already carries every readiness check main wants;
	// this package only mounts it, on Admin, never on Public.
	HealthRegistry *health.Registry

	// MetricsRegistry collects the HTTP request metrics NewHandler
	// registers into it; Admin mounts its scrape endpoint.
	MetricsRegistry *prometheus.Registry

	// DistFS is the built web-landing SPA (cmd/calbridge-landing's
	// embedded dist/, already stripped to its own root via fs.Sub),
	// served for every path that is not a real static asset.
	DistFS fs.FS

	// AppURL is the application's own public base URL, already
	// validated by landingconfig.Load. Every link this build renders
	// toward the application reads it back out of the served page; see
	// static.go's newStaticHandler.
	AppURL string

	// Operator is who the imprint and the privacy policy name, already
	// validated by landingconfig.Load. It reaches the pages the way
	// AppURL does, through the served index.html.
	Operator landingconfig.Operator
}

// languageCodes are the languages the site serves, the same two
// web-landing's router prefixes its legal pages with.
var languageCodes = []string{"de", "en"}

// legalPages are the legal pages the site has. A terms page exists
// because the consent screens of the sign-in providers link to one.
var legalPages = []string{"imprint", "privacy", "terms"}

// fallbackLanguage is the language a visitor with no stored choice and
// no matching Accept-Language header is redirected to.
const fallbackLanguage = "en"

// Handlers is what NewHandler builds: Public, served on the listener the
// landing Deployment's Ingress or HTTPRoute routes to, and Admin, served
// on govue's second, administrative listener.
type Handlers struct {
	Public http.Handler
	Admin  http.Handler
}

// NewHandler builds both handlers, returning every construction problem
// at once, the same pattern internal/httpapi.NewHandler and
// internal/config.Load both use.
func NewHandler(d Deps) (Handlers, error) {
	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	if d.Logger == nil {
		fail(errors.New("landingapi: logger is required"))
	}
	if d.HealthRegistry == nil {
		fail(errors.New("landingapi: health registry is required"))
	}
	if d.MetricsRegistry == nil {
		fail(errors.New("landingapi: metrics registry is required"))
	}
	if d.DistFS == nil {
		fail(errors.New("landingapi: dist filesystem is required"))
	}
	if d.AppURL == "" {
		fail(errors.New("landingapi: app URL is required"))
	}

	var staticHandler http.Handler
	if d.DistFS != nil {
		var err error
		staticHandler, err = newStaticHandler(d.DistFS, d.AppURL, d.Operator)
		fail(err)
	}

	if err := errors.Join(problems...); err != nil {
		return Handlers{}, fmt.Errorf("landingapi: build handler: %w", err)
	}

	security := middleware.DefaultSecurity()
	accessLog := middleware.AccessLog{}
	httpMetrics := metrics.NewHTTP(d.MetricsRegistry)
	metricsHandler := metrics.Handler(d.MetricsRegistry)

	mux := http.NewServeMux()
	mux.Handle("/", staticHandler)

	// The short legal paths (/imprint, /privacy, /terms and the German
	// names) redirect to the page in the visitor's language. govue's
	// language package owns that set and the language choice order:
	// cookie, then Accept-Language, then fallbackLanguage.
	//
	// Terms is on, which registers /terms and /agb together. The owner
	// decided on 2026-11-28 that every site that becomes a product has
	// terms, so calbridge has them although it sells nothing today. That
	// replaces issue #66's earlier line that /agb is not used. The terms
	// text is the terms of use of the service.
	if err := language.HandleLegal(mux, language.LegalConfig{
		Config: language.Config{Languages: languageCodes, Default: fallbackLanguage},
		Terms:  true,
	}, nil); err != nil {
		return Handlers{}, fmt.Errorf("landingapi: legal redirects: %w", err)
	}

	// The outer chain, outermost first, matching internal/httpapi's own
	// Public chain minus the per-route CSRF protector: there is no
	// unsafe route here for it to protect. See that package's doc
	// comment's "The middleware chain order" section for why each
	// position below is forced.
	public := middleware.Chain(mux,
		middleware.Recover(d.Logger),
		middleware.RequestID(d.Logger),
		security.Middleware,
		httpMetrics.Middleware,
		accessLog.Middleware,
	)

	// adminMux carries liveness, readiness and metrics, and nothing
	// else, for the same reason internal/httpapi's own adminMux does.
	adminMux := http.NewServeMux()
	d.HealthRegistry.Handle(adminMux)
	adminMux.Handle("GET /metrics", metricsHandler)

	admin := middleware.Chain(adminMux,
		middleware.Recover(d.Logger),
		middleware.RequestID(d.Logger),
	)

	return Handlers{Public: public, Admin: admin}, nil
}
