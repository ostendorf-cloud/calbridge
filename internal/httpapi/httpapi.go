// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package httpapi builds the HTTP handlers calbridge answers requests
// with: liveness, readiness and metrics on Admin, and on Public a session
// and CSRF contract shell (POST /api/v1/auth/login, GET /api/v1/me, POST
// /api/v1/auth/logout) plus the SPA fallback that serves the embedded Vue
// build. A later issue adds the calendar, edge, grant and webhook routes
// to Public.
//
// Every dependency is constructed by cmd/calbridge's main and handed to
// NewHandler through Deps; this package builds no logger, no health
// registry and no metrics registry of its own. The session manager and
// the CSRF signing key are the one exception: see "The session and CSRF
// contract" below for why this package builds both itself rather than
// taking them through Deps.
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
// Every route on Public gets a second, inner chain of its own: the CSRF
// protector, applied directly to the handler the mux dispatches to, the
// same reasoning middleware.RateLimit and metrics.HTTP.Middleware give
// for sitting on the mux rather than in the outer chain (see
// middleware.RateLimit's own doc comment). A later issue's webhook
// endpoint belongs on Public too, mounted ahead of the CSRF protector,
// since a provider's push notification carries no session and no token
// to check.
//
// Admin gets a deliberately short chain instead, matching govue's own
// adminHandler: Recover and RequestID only. A probe or a scrape is not
// browser traffic, so the security headers have nothing to protect, and
// a scraper polling this listener every few seconds must not fill the
// access log the way a public request would. The HTTP metrics
// middleware is absent too, deliberately: instrumenting a scrape of the
// metrics endpoint itself would feed Prometheus a metric about scraping
// Prometheus, which is not a loop worth closing.
//
// # The session and CSRF contract
//
// POST /api/v1/auth/login, GET /api/v1/me and POST /api/v1/auth/logout
// exist to prove govue's session and csrf packages are wired correctly,
// end to end against the Vue shell in web/: a login starts a session
// cookie, /me answers with the session's own csrf_token, and logout
// spends it on a CSRF-protected mutation. handleLogin takes nothing but
// an email and starts a session for it unconditionally: there is no
// account storage yet (see docs/concepts/calbridge.md's delivery order),
// so there is nothing to check a password or a provider identity
// against. Issue #5 replaces handleLogin wholesale with real local
// accounts and provider sign-in, built on internal/auth; it inherits
// this same session and CSRF wiring rather than rebuilding it.
//
// The session store is an in-memory session.MemoryStore and the CSRF key
// is generated fresh on every start with crypto/rand, both built inside
// NewHandler rather than taken through Deps: a session surviving a
// restart, or being configurable at all, is not a property this shell
// needs, and inventing a secret and a database-free persistence story
// for a handler #5 is about to replace is not worth doing twice.
// AllowPlainHTTP is forced on for the same reason this milestone has no
// TLS-terminating ingress of its own yet; a later issue that wires a real
// session configuration (see solobill's internal/config for the shape)
// replaces this constant along with the rest of this contract.
package httpapi

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/session"
)

// maxSessions bounds session.NewMemoryStore's tracked session count. This
// shell has no account system to drive it anywhere near govue's own
// default of 10000, sized for a SaaS with many concurrent users; a
// generous but bounded number keeps the worst case a known, small amount
// of memory instead of inheriting a bound sized for a different kind of
// deployment. See solobill's cmd/solobill for the same reasoning applied
// to a real deployment's own scale.
const maxSessions = 1000

// csrfKeyBytes is the length of the CSRF signing key NewHandler generates
// on every start, matching session.MinKeyBytes: a key shorter than this
// is a deployment mistake, not a value to run with anyway.
const csrfKeyBytes = session.MinKeyBytes

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

	// DistFS is the built SPA (cmd/calbridge's embedded dist/, already
	// stripped to its own root via fs.Sub), served as the fallback for
	// any unmatched GET that is not under apiPathPrefix.
	DistFS fs.FS
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
	if d.DistFS == nil {
		fail(errors.New("httpapi: dist filesystem is required"))
	}

	sessions, err := session.New(session.Config{
		Store:          session.NewMemoryStore(nil, maxSessions),
		AllowPlainHTTP: true,
	})
	fail(err)

	csrfKey := make([]byte, csrfKeyBytes)
	if _, err := rand.Read(csrfKey); err != nil {
		fail(fmt.Errorf("httpapi: generate csrf key: %w", err))
	}

	var protector *csrf.Protector
	if sessions != nil {
		protector, err = csrf.New(func(r *http.Request) (string, bool) {
			s, ok, err := sessions.Read(r.Context(), r)
			if err != nil || !ok {
				return "", false
			}

			return csrf.Token(csrfKey, s.ID), true
		})
		fail(err)
		if protector != nil {
			protector.OnRefusal(func(w http.ResponseWriter, r *http.Request, _ error) {
				api.WriteError(w, r, api.PermissionDenied("cross origin or stale request, please reload"))
			})
		}
	}

	var spaHandler http.Handler
	if d.DistFS != nil {
		spaHandler, err = newSPAHandler(d.DistFS)
		fail(err)
	}

	if err := errors.Join(problems...); err != nil {
		return Handlers{}, fmt.Errorf("httpapi: build handler: %w", err)
	}

	a := &app{sessions: sessions, csrfKey: csrfKey}

	security := middleware.DefaultSecurity()
	accessLog := middleware.AccessLog{}
	httpMetrics := metrics.NewHTTP(d.MetricsRegistry)
	metricsHandler := metrics.Handler(d.MetricsRegistry)

	// route is the one per-handler chain: the CSRF protector, applied to
	// every route alike. It must run on the handler the mux dispatches
	// to, not in the outer chain: see the package doc's "The middleware
	// chain order" section.
	route := func(h http.Handler) http.Handler {
		return middleware.Chain(h, protector.Middleware)
	}

	mux := http.NewServeMux()

	mux.Handle("POST /api/v1/auth/login", route(api.Handle(a.handleLogin)))
	mux.Handle("GET /api/v1/me", route(api.Handle(a.handleMe)))
	mux.Handle("POST /api/v1/auth/logout", route(api.Handle(a.handleLogout)))

	// /metrics, /healthz and /readyz are deliberately registered here as
	// a 404, not left to fall through to the SPA catch-all below: Admin
	// serves the real answers instead (see the package doc's "Two
	// handlers, two listeners" section), and the SPA fallback answers
	// every unrecognised path outside apiPathPrefix with its own shell
	// and a 200, which would otherwise hide the fact that nothing real
	// is behind these three paths on this listener. Registered with no
	// method, so every method gets the 404, not only GET, and as both
	// the exact path and its trailing-slash subtree, so a path under it
	// cannot fall through to the catch-all either.
	for _, path := range [...]string{"/metrics", "/healthz", "/readyz"} {
		mux.Handle(path, route(api.NotFoundHandler()))
		mux.Handle(path+"/", route(api.NotFoundHandler()))
	}

	// The SPA fallback. It answers an unmatched /api/ path with the same
	// JSON error shape every other endpoint uses, and everything else
	// with the built Vue app, for client-side routing to take over.
	mux.Handle("/", route(spaHandler))

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

// app holds the dependencies the handlers in this package call.
type app struct {
	sessions *session.Manager
	csrfKey  []byte
}

// loginRequest is handleLogin's body: an email and nothing else. See the
// package doc's "The session and CSRF contract" section for why there is
// no password, no TOTP code and no provider identity to check yet.
type loginRequest struct {
	Email string `json:"email"`
}

// meResponse is what handleMe answers a valid session with: the email the
// session was started for, and the CSRF token that same session's id
// derives, for the client to spend on its next unsafe request.
type meResponse struct {
	Email     string `json:"email"`
	CSRFToken string `json:"csrf_token"`
}

// handleLogin starts a session for the email a request names, with no
// check beyond it being present: see the package doc's "The session and
// CSRF contract" section.
func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := api.Decode(r, &req); err != nil {
		return err
	}

	email := strings.TrimSpace(req.Email)
	if email == "" {
		return api.Invalid("email is required")
	}

	if _, err := a.sessions.Start(r.Context(), w, session.Session{Email: email}); err != nil {
		return fmt.Errorf("httpapi: start session: %w", err)
	}

	api.NoContent(w)

	return nil
}

// handleMe answers the session a request carries, or api.Unauthenticated
// when it carries none.
func (a *app) handleMe(w http.ResponseWriter, r *http.Request) error {
	s, ok, err := a.sessions.Read(r.Context(), r)
	if err != nil {
		return api.Unavailable("session store is unavailable").WithCause(err)
	}
	if !ok {
		return api.Unauthenticated("log in first")
	}

	return api.Write(w, http.StatusOK, meResponse{
		Email:     s.Email,
		CSRFToken: csrf.Token(a.csrfKey, s.ID),
	})
}

// handleLogout ends the session a request carries, if any. Reaching this
// handler at all already proved the CSRF token the request carried
// matched the session's own, since this route is behind the CSRF
// protector like every other.
func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if err := a.sessions.End(r.Context(), w, r); err != nil {
		return fmt.Errorf("httpapi: end session: %w", err)
	}

	api.NoContent(w)

	return nil
}
