// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package httpapi builds the HTTP handlers calbridge answers requests
// with: liveness, readiness and metrics on Admin, and on Public the
// signup, login, sign-in-with-a-provider, session and account routes
// listed under "The session and CSRF contract" below, the sync graph
// routes the Vue Flow canvas is a view of, and the SPA fallback that
// serves the embedded Vue build. A later issue adds the webhook route to
// Public. The graph routes live in graph.go, and the three rules every
// one of them holds to are written out in that file's own doc comment.
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
// middleware.RateLimit's own doc comment). The unauthenticated routes
// get the rate limiter ahead of the protector besides: those are the
// routes a request needs no session to reach at all, see "The session
// and CSRF contract" below. A later issue's webhook endpoint belongs on
// Public too, mounted ahead of the CSRF protector, since a provider's
// push notification carries no session and no token to check.
//
// The two provider sign-in routes stay behind the protector like every
// other, even though one of them is reached by a cross-site redirect
// from the identity provider: csrf.Protector checks a token only on an
// unsafe method, and both are GET, so the protector's origin half is
// all that runs and net/http's own CrossOriginProtection allows a safe
// method unconditionally. Mounting them outside the protector would
// have been a change to the CSRF wiring for no gain.
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
// Public carries these routes, all of them thin: every decision about
// an account belongs to internal/auth, and this package only decodes a
// request, calls that package and turns what comes back into JSON or a
// session cookie.
//
//	POST   /api/v1/auth/signup                      create a local account and return its TOTP enrolment
//	POST   /api/v1/auth/login                       email, password and TOTP code; starts the session
//	POST   /api/v1/auth/logout                      ends the session
//	GET    /api/v1/me                               who the session is for, and its csrf_token
//	DELETE /api/v1/me                               deletes the account and everything it owns
//	GET    /api/v1/auth/providers                   the sign-in providers this deployment configured
//	GET    /api/v1/auth/providers/{provider}/login     starts a provider sign-in
//	GET    /api/v1/auth/providers/{provider}/callback  finishes one and starts the session
//	GET    /api/v1/graph                            the whole sync graph: accounts, calendars, edges, grants, presets
//	POST   /api/v1/edges                            draws one edge, into an existing target or one calbridge creates
//	PATCH  /api/v1/edges/{id}                       changes an edge's detail level
//	DELETE /api/v1/edges/{id}                       removes one edge
//	POST   /api/v1/grants                           shares one calendar with another account, up to a detail level
//	DELETE /api/v1/grants/{id}                      revokes one grant, from either side
//
// A login starts a session cookie, /me answers with the session's own
// csrf_token, and every unsafe route spends it. Both login paths end at
// the identical call, sessions.Start(ctx, w, identity.Session()), so
// nothing downstream can tell a local login from a provider one; see
// internal/auth's Identity.
//
// Signup, login and the two provider routes are the ones a request can
// reach with no session at all, which is why they get the rate limiter
// ahead of the CSRF protector: see config.Config's RateLimitBurst and
// RateLimitPer, and maxSessions below for the budget that limiter is
// sized against. Signup and the provider callback both create rows, so
// the same limit is what keeps a script from filling the users table.
//
// The session store is an in-memory session.MemoryStore, and the CSRF
// signing key and the session.Codec that signs the provider login in
// flight are both generated fresh on every start with crypto/rand, all
// built inside NewHandler rather than taken through Deps. A session
// surviving a restart is not a property this deployment has yet (a
// restart signs everybody out, and a sign-in that was mid-redirect
// across one starts over), and a persistent session store is its own
// piece of work rather than something to bolt on here. AllowPlainHTTP,
// by contrast, comes through Deps from
// config.Config.SessionAllowPlainHTTP: it defaults to false, the secure
// choice, since charts/calbridge can expose the service through a
// TLS-terminating Ingress or HTTPRoute, and a plain HTTP docker-compose
// run opts in explicitly rather than inheriting it as the shipped
// default.
//
// # Provider sign-in
//
// A provider sign-in asks for identity scopes and never for a calendar
// scope; internal/auth fixes that list and this package cannot widen it.
// Apple is offered and grants identity alone, which the login screen
// says where the person decides: GET /api/v1/auth/providers carries the
// identity_only flag the UI renders that from.
//
// ProviderLoginPath and ProviderCallbackPath below are the two routes
// mounted per configured provider. The callback path is also what
// cmd/calbridge's main builds each provider's redirect URL from, rather
// than a second spelling of the same path in configuration that could
// drift from the route actually mounted here.
package httpapi

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/metrics"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/oidc"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/session"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// maxSessions bounds session.NewMemoryStore's tracked session count.
// This deployment is a single replica serving a handful of accounts,
// nowhere near govue's own default of 10000, sized for a SaaS with many
// concurrent users; a generous but bounded number keeps the worst case a
// known, small amount of memory instead of inheriting a bound sized for
// a different kind of deployment. See solobill's cmd/solobill for the
// same reasoning applied to a real deployment's own scale.
//
// The number is deliberate only together with the login rate limiter
// below. session.MemoryStore.Save evicts an arbitrary tracked session
// once the store is full, so a login that could be attempted without
// limit would let one client force 1000 evictions, and sign everybody
// else out, in well under a minute. A session is now only started by a
// login that actually verified a password and a TOTP code, or by a
// provider callback the identity provider vouched for, so filling this
// store takes credentials rather than a loop. Capped on top of that at
// config.Config's default of ten attempts a minute for one resolved
// client address, that address needs 99 minutes to cycle through the
// whole store even if every attempt succeeded: 10 immediately from the
// full burst, then the remaining 990 at one every six seconds
// (990 * 6s = 5940s = 99m).
//
// A flood spread across many distinct client addresses is a different
// threat this bound does not close, and maxRateLimitKeys below does not
// close it either: with 1000 sessions tracked against 100000 rate limit
// keys, a thousand distinct addresses each get their own full burst.
// Each of those attempts still has to carry credentials that work, so
// the distributed case needs a thousand accounts rather than a thousand
// addresses; a persistent session store, which this deployment does not
// have yet, is what removes the bound rather than widening it.
const maxSessions = 1000

// maxRateLimitKeys bounds the number of distinct client addresses the
// login rate limiter tracks at once, matching govue's own example
// application and solobill's internal/httpapi: a flood of one-shot
// addresses must not grow that map without bound either. See
// maxSessions's own doc comment for why this number being far larger
// than maxSessions does not by itself close a flood spread across many
// addresses.
const maxRateLimitKeys = 100_000

// csrfKeyBytes is the length of the CSRF signing key, and of the
// session.Codec signing key, that NewHandler generates on every start,
// matching session.MinKeyBytes: a key shorter than this is a deployment
// mistake, not a value to run with anyway.
const csrfKeyBytes = session.MinKeyBytes

// ProviderLoginPath and ProviderCallbackPath are the two routes a
// configured sign-in provider is mounted at. cmd/calbridge's main builds
// each provider's registered redirect URL from ProviderCallbackPath, so
// the URL a provider is told to come back to and the route that answers
// it can never drift apart.
func ProviderLoginPath(providerID string) string {
	return apiPathPrefix + "v1/auth/providers/" + providerID + "/login"
}

// ProviderCallbackPath is where a provider sends the browser back to.
func ProviderCallbackPath(providerID string) string {
	return apiPathPrefix + "v1/auth/providers/" + providerID + "/callback"
}

// Store is what this package needs from a storage backend. It is
// defined here, at the consumer, per go-conventions:
// internal/storage/postgres.Store satisfies it with no adapter, because
// its method set is a superset of this one.
//
// It embeds internal/auth's own Store, so the account routes keep
// reaching exactly what that package allows and nothing more: there is
// still no connection repository on auth.Store, so no code in
// internal/auth can reach one, and widening this interface does not
// widen that one.
//
// The repositories added here are the sync graph's. Every one of them
// requires a context storage.UserScope.WithUser produced, which is what
// makes row level security, and not a conditional in a handler, the
// thing that decides what a request can see; see graph.go's inUserScope.
type Store interface {
	auth.Store

	Connections() storage.ConnectionRepository
	Calendars() storage.CalendarRepository
	Edges() storage.EdgeRepository
	CalendarGrants() storage.CalendarGrantRepository
	Sharing() storage.SharingRepository
}

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

	// AllowPlainHTTP mirrors config.Config.SessionAllowPlainHTTP: it drops
	// the session cookie's Secure attribute and the security headers'
	// HSTS, for a plain HTTP development run. Left false, the default,
	// for anything reachable from outside the machine it runs on.
	AllowPlainHTTP bool

	// RateLimitBurst and RateLimitPer configure the token bucket the
	// login route is rate limited with. See maxSessions's own doc
	// comment for why that route specifically needs one.
	RateLimitBurst int
	RateLimitPer   time.Duration

	// TrustedProxyCIDRs mirrors config.Config.TrustedProxyCIDRs: the
	// addresses the login rate limiter's clientip.Resolver trusts to have
	// appended the real client address to X-Forwarded-For. Left empty,
	// the resolver keys every request on its TCP peer; see
	// config.Config.TrustedProxyCIDRs's own doc comment for what that
	// means behind this application's Ingress or HTTPRoute.
	TrustedProxyCIDRs []netip.Prefix

	// Store is the storage backend every route runs against: the account
	// routes through internal/auth, and the sync graph routes directly,
	// always inside Store.WithUser. Required: without it there is no
	// account to check a login against, and an application that answered
	// a login anyway is the shell this package no longer is.
	Store Store

	// AuthProviders are the sign-in providers this deployment
	// configured, already built by main from config.Config.AuthProviders.
	// Empty is a deployment offering local accounts only, which is a
	// supported shape, not a misconfiguration. Each one is mounted at
	// ProviderLoginPath and ProviderCallbackPath for its own id.
	AuthProviders []*auth.Provider

	// Now is the clock the TOTP check runs against. Nil is time.Now; a
	// test passes a fixed one.
	Now func() time.Time
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
	if d.Store == nil {
		fail(errors.New("httpapi: store is required"))
	}

	sessions, err := session.New(session.Config{
		Store:          session.NewMemoryStore(nil, maxSessions),
		AllowPlainHTTP: d.AllowPlainHTTP,
	})
	fail(err)

	limiter := ratelimit.New(d.RateLimitBurst, d.RateLimitPer, maxRateLimitKeys)
	rateLimit, err := middleware.NewRateLimit(middleware.RateLimitConfig{
		Limiter: limiter,
		// d.TrustedProxyCIDRs is empty by default, so this resolver
		// trusts no proxy and keys every request on its TCP peer. See
		// config.Config.TrustedProxyCIDRs's own doc comment for why
		// that is the safe default and what it costs behind this
		// application's Ingress or HTTPRoute.
		ClientIP: clientip.NewResolver(d.TrustedProxyCIDRs),
	})
	fail(err)

	csrfKey := make([]byte, csrfKeyBytes)
	if _, err := rand.Read(csrfKey); err != nil {
		fail(fmt.Errorf("httpapi: generate csrf key: %w", err))
	}

	// logins signs the provider login in flight: the state, the nonce
	// and the PKCE verifier of a sign-in that has not happened yet, which
	// have to survive the redirect to the provider and back at a point
	// where there is no session to keep them in. Its key is generated
	// here for the same reason the CSRF key above is; see the package
	// doc.
	loginKey := make([]byte, csrfKeyBytes)
	if _, err := rand.Read(loginKey); err != nil {
		fail(fmt.Errorf("httpapi: generate login signing key: %w", err))
	}

	logins, err := session.NewCodec(session.CodecConfig{
		Key:            loginKey,
		AllowPlainHTTP: d.AllowPlainHTTP,
	})
	fail(err)

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

	a := &app{
		sessions: sessions,
		csrfKey:  csrfKey,
		store:    d.Store,
		local:    auth.NewLocal(d.Store, d.Now),
		logger:   d.Logger,
	}

	providerHandlers := make(map[string]*oidc.Handlers, len(d.AuthProviders))

	for _, provider := range d.AuthProviders {
		id := string(provider.ID())

		handlers, err := auth.NewProviderHandlers(provider, auth.ProviderHandlerConfig{
			Logins:   logins,
			Sessions: sessions,
			Store:    d.Store,
			// The SPA's own entry point: a finished sign-in lands there
			// and the client routes from there, which is also why
			// internal/auth sets govue's NoNext.
			Success: "/",
			OnError: func(w http.ResponseWriter, r *http.Request, err error) {
				api.WriteError(w, r, providerSignInError(err))
			},
		})
		if err != nil {
			return Handlers{}, fmt.Errorf("httpapi: build handler: %w", err)
		}

		providerHandlers[id] = handlers

		a.providers = append(a.providers, provider.Info())
	}

	a.providerHandlers = providerHandlers

	security := middleware.DefaultSecurity()
	if d.AllowPlainHTTP {
		// A browser reached over plain HTTP must never be told to
		// upgrade every future visit to HTTPS.
		security.HSTS = 0
	}
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

	// openRoute adds the rate limiter ahead of the CSRF protector, for
	// the routes a request needs no session to reach at all. See
	// maxSessions's own doc comment for what an unthrottled attempt
	// against one of them would let a client do.
	openRoute := func(h http.Handler) http.Handler {
		return middleware.Chain(h, rateLimit.Middleware, protector.Middleware)
	}

	// limitedRoute is the same chain as openRoute, for a route that does
	// need a session and still has to be throttled. There is exactly one:
	// POST /api/v1/grants, which names the account to share with by email
	// address and answers "no account exists for this email address" when
	// there is none.
	//
	// That answer is deliberate and is argued in graph.go's
	// handleCreateGrant: it discloses nothing signup does not already,
	// since signup answers "an account already exists for this email
	// address". The argument only holds while the two are bounded the
	// same way, though. Signup carries this limiter; without it here, a
	// caller with one account could enumerate the whole user base by
	// address far faster than through signup, and the disclosure would
	// stop being equivalent and start being the cheaper path.
	limitedRoute := func(h http.Handler) http.Handler {
		return middleware.Chain(h, rateLimit.Middleware, protector.Middleware)
	}

	mux := http.NewServeMux()

	mux.Handle("POST /api/v1/auth/signup", openRoute(api.Handle(a.handleSignUp)))
	mux.Handle("POST /api/v1/auth/login", openRoute(api.Handle(a.handleLogin)))
	mux.Handle("POST /api/v1/auth/logout", route(api.Handle(a.handleLogout)))
	mux.Handle("GET /api/v1/auth/providers", route(api.Handle(a.handleProviders)))
	mux.Handle("GET /api/v1/me", route(api.Handle(a.handleMe)))
	mux.Handle("DELETE /api/v1/me", route(api.Handle(a.handleDeleteAccount)))

	// The sync graph. Every one of these needs a session and runs inside
	// Store.WithUser; see graph.go. All but one carry no rate limiter,
	// unlike signup and login: a request with no session never reaches
	// past sessionFromRequest, so there is nothing an unauthenticated
	// caller can spend by repeating one, and a caller that does have a
	// session is already named. The exception is the grant route, which
	// is mounted through limitedRoute above for the reason recorded
	// there.
	mux.Handle("GET /api/v1/graph", route(api.Handle(a.handleGraph)))
	mux.Handle("POST /api/v1/edges", route(api.Handle(a.handleCreateEdge)))
	mux.Handle("PATCH /api/v1/edges/{id}", route(api.Handle(a.handleUpdateEdge)))
	mux.Handle("DELETE /api/v1/edges/{id}", route(api.Handle(a.handleDeleteEdge)))
	mux.Handle("POST /api/v1/grants", limitedRoute(api.Handle(a.handleCreateGrant)))
	mux.Handle("DELETE /api/v1/grants/{id}", route(api.Handle(a.handleDeleteGrant)))

	// One pair of routes per configured provider, mounted by name rather
	// than behind a single {provider} wildcard: a path segment that
	// reaches a handler before anything has checked it is one more thing
	// to validate, and an unconfigured provider answers with the SPA's
	// own JSON 404 for free this way.
	for id, handlers := range providerHandlers {
		mux.Handle("GET "+ProviderLoginPath(id), openRoute(handlers.Login()))
		mux.Handle("GET "+ProviderCallbackPath(id), openRoute(handlers.Callback()))
	}

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
	store    Store
	local    *auth.Local
	logger   *slog.Logger

	// providers is what GET /api/v1/auth/providers answers with, built
	// once at startup; providerHandlers is the pair of govue handlers
	// each of them is mounted as, keyed by provider id.
	providers        []auth.ProviderInfo
	providerHandlers map[string]*oidc.Handlers
}
