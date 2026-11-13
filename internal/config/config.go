// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package config loads calbridge's configuration from the environment
// once, at startup, and validates it fully before anything listens: see
// Load.
//
// Every value, secret included, is read through govue's envconf.Loader.
// A secret's own variable (for example DATABASE_PASSWORD) may carry the
// value directly, or the same name with a "_FILE" suffix
// (DATABASE_PASSWORD_FILE) may point at a file holding it, and setting
// both at once is an error rather than a silent precedence rule: that is
// the shape a Kubernetes Secret mounted as a file and a plain environment
// value both need to fit through the same configuration surface. That
// contract is envconf.Loader.SecretOrFile and RequiredSecretOrFile.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/envconf"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
)

// encryptionKeyBytes is the exact decoded length Load requires for
// ENCRYPTION_KEY, matching internal/storage/postgres's AES-256 key
// length. Unlike a signing key, which only needs to be at least some
// minimum length, an AES-256 key must be exactly 32 bytes: a wrong
// length is a deployment mistake to fail loudly on rather than stretch
// or truncate.
const encryptionKeyBytes = 32

// defaultRateLimitBurst and defaultRateLimitPer throttle the one route a
// request needs no session to reach: POST /api/v1/auth/login starts a
// session for any email with no account to check it against (see
// internal/httpapi's package doc), so without a limit a single client
// could force session.MemoryStore's eviction of an arbitrary tracked
// session, repeatedly, in seconds. Ten starts a minute for one resolved
// client address is generous for a browser and tight for a script, the
// same budget solobill's own local login uses for the same reason. That
// resolved address is the TCP peer unless TrustedProxyCIDRs below names
// the deployment's own proxy: behind charts/calbridge's Ingress or
// HTTPRoute with no proxy configured, the TCP peer is the ingress
// controller for every request, so the budget is shared by every client
// instead of kept one per client. See TrustedProxyCIDRs's own doc
// comment and the chart's README.
const (
	defaultRateLimitBurst = 10
	defaultRateLimitPer   = time.Minute
)

// Config is every setting calbridge reads from the environment, loaded
// and validated once by Load.
type Config struct {
	// ListenAddr is where the public HTTP handler in internal/httpapi
	// listens.
	ListenAddr string
	// AdminListenAddr is the second, administrative listener govue's
	// httpserver.RunAdmin serves alongside ListenAddr. Liveness,
	// readiness and metrics are served there and nowhere on ListenAddr,
	// so nothing routed through ListenAddr's Ingress or HTTPRoute ever
	// reaches them.
	AdminListenAddr string
	// DrainDelay is how long httpserver.Serve waits, after
	// healthRegistry.Drain runs and before either listener stops
	// accepting new connections, for a readiness probe to notice and a
	// load balancer to stop sending new requests. It must stay above the
	// readiness probe's own periodSeconds times failureThreshold, or a
	// probe can still be sending traffic to a listener that has already
	// closed; charts/calbridge keeps the two in that order.
	DrainDelay time.Duration
	// LogLevel is the structured logger's minimum level, built once in
	// cmd/calbridge's main and injected everywhere else.
	LogLevel slog.Level

	// DatabaseURL is the connection URL without credentials: scheme,
	// host, port, database name and query parameters only. DatabaseUser
	// and DatabasePassword are assembled into it by
	// internal/storage/postgres.NewPoolWithCredentials, rather than
	// three different ways of saying the same thing.
	DatabaseURL      string
	DatabaseUser     string
	DatabasePassword string

	// EncryptionKey is the raw AES-256 key internal/storage/postgres
	// encrypts connections.credentials and event_mapping.encrypted_details
	// with. Where it comes from in a real cluster is still an open
	// question docs/concepts/calbridge.md records; this field only
	// carries whatever the environment supplied.
	EncryptionKey []byte

	// SessionAllowPlainHTTP drops the Secure attribute (and the __Host-
	// cookie prefix it is a precondition of) from the session cookie,
	// which is what lets a session survive a plain HTTP docker-compose
	// run during local development. The default is false: charts/calbridge
	// can expose the service through a TLS-terminating Ingress or
	// HTTPRoute, and a real deployment must leave this false, since
	// without Secure the session id travels in the clear.
	SessionAllowPlainHTTP bool

	// RateLimitBurst and RateLimitPer configure the token bucket
	// internal/httpapi applies to the login route.
	RateLimitBurst int
	RateLimitPer   time.Duration

	// TrustedProxyCIDRs lists the addresses internal/httpapi's login rate
	// limiter trusts to have appended the real client address to
	// X-Forwarded-For, parsed by govue's clientip.ParseCIDRs. Empty by
	// default: the limiter then keys every request on its TCP peer, the
	// secure choice, since trusting the header from an unconfigured proxy
	// would let a request forge its own rate limit key. charts/calbridge
	// routes every request through its own Ingress or HTTPRoute, so
	// without this set the TCP peer calbridge sees is the ingress
	// controller, not the client, and the limiter's budget ends up shared
	// by every client instead of kept one per client. Setting this to the
	// ingress controller's (or Gateway data plane's) address lets the
	// limiter trust the header that controller appends and recover the
	// real client address; see the chart's README for how to set it.
	TrustedProxyCIDRs []netip.Prefix

	// AuthBaseURL is this deployment's own public base URL, scheme and
	// host, with no path. Provider sign-in redirect URLs are built from
	// it (see AuthProvider.RedirectURL), and it has to match what is
	// registered at the provider exactly, because the provider compares
	// them literally. It is required only when at least one provider is
	// configured, and empty otherwise.
	AuthBaseURL string

	// AuthProviders carries the sign-in providers this deployment
	// configured, in the fixed order of authProviderIDs. A provider with
	// no client id is simply not offered; internal/auth and
	// internal/httpapi read this slice and nothing else to decide which
	// buttons exist.
	AuthProviders []AuthProvider
}

// AuthProvider is one sign-in provider's client registration.
// ClientSecret follows the same _FILE convention every other secret
// here does, has no default, and must never be logged or put into an
// error; see this package's own doc comment and internal/auth's
// ProviderConfig.
type AuthProvider struct {
	// ID is "google", "microsoft" or "apple", matching
	// storage.IdentityProvider's own values.
	ID string

	// ClientID is this application's client at the provider. A provider
	// whose client id is empty is left out of AuthProviders entirely.
	ClientID string

	// ClientSecret authenticates the token request.
	ClientSecret string

	// Issuer overrides internal/auth's own default issuer for this
	// provider, which is what a deployment restricted to one Microsoft
	// Entra ID tenant needs. Empty keeps that default.
	Issuer string
}

// authProviderIDs is every provider a sign-in can be configured for, and
// the order AuthProviders keeps them in, so a sign-in screen's buttons do
// not reorder themselves between restarts.
var authProviderIDs = [...]string{"google", "microsoft", "apple"}

// Load reads every setting through getenv, collecting every problem into
// one error so a misconfigured deployment reports all of its mistakes on
// the first start instead of one per restart. A nil getenv reads the real
// process environment through os.Getenv.
func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	l := envconf.New(getenv)

	var problems []error
	fail := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	cfg := Config{
		ListenAddr:      l.String("LISTEN_ADDR", ":8080"),
		AdminListenAddr: l.String("ADMIN_LISTEN_ADDR", ":8081"),
	}

	cfg.DrainDelay = l.Duration("DRAIN_DELAY", httpserver.DefaultDrainDelay, time.Second, 5*time.Minute)

	level, err := logging.Level(l.String("LOG_LEVEL", "info"))
	fail(err)
	cfg.LogLevel = level

	cfg.DatabaseURL = l.Required("DATABASE_URL")
	cfg.DatabaseUser = l.Required("DATABASE_USER")
	cfg.DatabasePassword = l.RequiredSecretOrFile("DATABASE_PASSWORD")

	encryptionKey, err := decodeHexKey("ENCRYPTION_KEY", l.SecretOrFile("ENCRYPTION_KEY"), encryptionKeyBytes)
	fail(err)
	cfg.EncryptionKey = encryptionKey

	cfg.SessionAllowPlainHTTP = l.Bool("SESSION_ALLOW_PLAIN_HTTP", false)

	cfg.RateLimitBurst = l.Int("RATE_LIMIT_BURST", defaultRateLimitBurst, 0, 100_000)
	cfg.RateLimitPer = l.Duration("RATE_LIMIT_PER", defaultRateLimitPer, time.Second, time.Hour)

	trustedProxyCIDRs, err := clientip.ParseCIDRs(l.String("TRUSTED_PROXY_CIDRS", ""))
	fail(err)
	cfg.TrustedProxyCIDRs = trustedProxyCIDRs

	cfg.AuthBaseURL = strings.TrimSuffix(strings.TrimSpace(l.String("AUTH_BASE_URL", "")), "/")

	providers, err := loadAuthProviders(l, cfg.AuthBaseURL)
	fail(err)
	cfg.AuthProviders = providers

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("config: load: %w", err)
	}

	return cfg, nil
}

// loadAuthProviders reads each provider's client registration and keeps
// the ones a deployment actually configured, in authProviderIDs' order.
//
// A provider is configured by its client id: empty means the provider is
// not offered, which is the ordinary case for a deployment that only
// wants local accounts. A client secret without a client id, and a
// client id without a client secret, are both deployment mistakes rather
// than a half-configured provider to silently skip, so each is reported.
//
// No error here ever carries a secret's value: a message names the
// variable, never what was in it.
func loadAuthProviders(l *envconf.Loader, baseURL string) ([]AuthProvider, error) {
	var (
		providers []AuthProvider
		problems  []error
	)

	for _, id := range authProviderIDs {
		prefix := "AUTH_" + strings.ToUpper(id) + "_"

		clientID := strings.TrimSpace(l.String(prefix+"CLIENT_ID", ""))
		clientSecret := l.SecretOrFile(prefix + "CLIENT_SECRET")

		switch {
		case clientID == "" && clientSecret == "":
			continue
		case clientID == "":
			problems = append(problems, fmt.Errorf("%sCLIENT_SECRET is set without %sCLIENT_ID", prefix, prefix))

			continue
		case clientSecret == "":
			problems = append(problems, fmt.Errorf("%sCLIENT_SECRET is required once %sCLIENT_ID is set", prefix, prefix))

			continue
		}

		providers = append(providers, AuthProvider{
			ID:           id,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Issuer:       strings.TrimSpace(l.String(prefix+"ISSUER", "")),
		})
	}

	if len(providers) > 0 {
		problems = append(problems, checkAuthBaseURL(baseURL))
	}

	return providers, errors.Join(problems...)
}

// checkAuthBaseURL requires an absolute http or https URL with a host and
// no path once any provider is configured. A redirect URL built from
// anything else is one the provider will refuse at the callback, which is
// a failure far from its cause.
func checkAuthBaseURL(baseURL string) error {
	if baseURL == "" {
		return errors.New("AUTH_BASE_URL is required once a sign-in provider is configured")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("AUTH_BASE_URL: %w", err)
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("AUTH_BASE_URL: %q is not an absolute http or https URL", baseURL)
	}

	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("AUTH_BASE_URL: %q must carry scheme and host only, no path", baseURL)
	}

	return nil
}

// decodeHexKey decodes raw as hex and checks that it is exactly
// wantBytes long. The key is generated, not typed, so a wrong length is
// a deployment mistake rather than a length to stretch to. An empty raw
// (neither the variable nor its _FILE twin set) decodes to zero bytes
// and fails the length check below with a message naming the right
// variable, so no separate "is required" check is needed for it.
func decodeHexKey(name, raw string, wantBytes int) ([]byte, error) {
	key, err := hex.DecodeString(raw)
	if err != nil {
		// Not wrapped with %w: hex.DecodeString's own error
		// (hex.InvalidByteError) quotes the offending byte of the key
		// material itself, which this message must not repeat.
		return nil, fmt.Errorf("%s: must be hex encoded", name)
	}

	if len(key) != wantBytes {
		return nil, fmt.Errorf("%s: decodes to %d bytes, exactly %d are required", name, len(key), wantBytes)
	}

	return key, nil
}
