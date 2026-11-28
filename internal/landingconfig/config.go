// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Package landingconfig loads the public landing server's configuration
// from the environment once, at startup, through govue's envconf,
// mirroring internal/config's own shape at a fraction of its size: the
// landing build carries no database, no session and no sign-in
// provider, so there is nothing here to load for any of those.
package landingconfig

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/envconf"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
)

// Config is every setting the landing server reads from the
// environment, loaded and validated once by Load.
type Config struct {
	// ListenAddr is where the public handler listens.
	ListenAddr string
	// AdminListenAddr is the second, administrative listener govue's
	// httpserver.RunAdmin serves alongside ListenAddr: liveness,
	// readiness and metrics live there and nowhere on ListenAddr. See
	// internal/config.Config.AdminListenAddr for the full reasoning,
	// reproduced here unchanged.
	AdminListenAddr string
	// DrainDelay mirrors internal/config.Config.DrainDelay: how long
	// httpserver.Serve waits after the health registry starts draining
	// before either listener stops accepting new connections.
	DrainDelay time.Duration
	// LogLevel is the structured logger's minimum level.
	LogLevel slog.Level

	// AppURL is the application's own public base URL, scheme and host,
	// with no path, for example "https://app.calbridge.example.com".
	// internal/landingapi renders every link away from this build
	// toward it, so a correction to that host never means a code
	// change. Required: a landing page whose only call to action points
	// nowhere is a deployment mistake to fail loudly on at startup
	// rather than serve.
	AppURL string

	// Operator is who the imprint and the privacy policy name. Required:
	// a landing page whose legal pages carry a placeholder, or nothing,
	// must not be served, because the consent screens of the sign-in
	// providers link to them. See Operator.
	Operator Operator
}

// Load reads every setting through getenv, collecting every problem
// into one error so a misconfigured deployment reports all of its
// mistakes on the first start instead of one per restart. A nil getenv
// reads the real process environment through os.Getenv.
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

	// l.URL already requires an absolute http or https URL with a host,
	// no credentials, no query and no fragment, and trims a trailing
	// slash; the empty fallback is what makes it required here, the
	// same shape internal/config.checkAuthBaseURL enforces by hand for
	// AUTH_BASE_URL. The one check it leaves to this package is the
	// path: a landing link must carry scheme and host only, since
	// nothing in this build knows a sub-path to append one to.
	//
	// The one shape l.URL reports as the wrong problem is a value that
	// forgot its scheme and carries credentials anyway
	// ("user:secret@host"): url.Parse reads "user" as the scheme, leaves
	// User nil, and l.URL then refuses it for not starting with http://
	// while quoting the whole value, password included. That shape is
	// caught here first, and the value never reaches l.URL, so only the
	// one refusal naming the right problem is reported.
	if raw := l.String("APP_URL", ""); namesCredentialsBeforeHost(raw) {
		fail(errors.New("APP_URL: must not contain credentials"))
	} else if appURL := l.URL("APP_URL", ""); appURL != nil {
		if appURL.Path != "" {
			fail(fmt.Errorf("APP_URL: %q must carry scheme and host only, no path", appURL.String()))
		} else {
			cfg.AppURL = appURL.String()
		}
	}

	operator, err := loadOperator(l)
	fail(err)
	cfg.Operator = operator

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("landingconfig: load: %w", err)
	}

	return cfg, nil
}

// namesCredentialsBeforeHost reports whether a value puts user
// information in front of its host, whether or not it carries a scheme.
// url.Parse only fills User for a value with a "//" authority, so
// "https://user:secret@host" is caught by envconf's own check while
// "user:secret@host", the same value with the scheme forgotten, is not:
// that one parses as the scheme "user" with the opaque part
// "secret@host". Both are the same deployment mistake, both are refused
// with the same message, and the message names no part of the value.
//
// internal/config carries the same function for PUBLIC_SITE_URL rather
// than this package importing it or the other way round: the landing
// binary reaches internal/landingapi and internal/landingconfig and
// nothing else of this module, and
// internal/depsync/landingimports_builtfrontend_test.go fails if that
// changes. The two
// copies are kept honest by a refusal test on each side.
func namesCredentialsBeforeHost(raw string) bool {
	rest := strings.TrimSpace(raw)

	if scheme, afterScheme, found := strings.Cut(rest, "://"); found && !strings.ContainsAny(scheme, "/?#@") {
		rest = afterScheme
	}

	authority := rest
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}

	return strings.Contains(authority, "@")
}
