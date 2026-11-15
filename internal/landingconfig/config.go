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
	appURL := l.URL("APP_URL", "")
	if appURL != nil && appURL.Path != "" {
		fail(fmt.Errorf("APP_URL: %q must carry scheme and host only, no path", appURL.String()))
	} else if appURL != nil {
		cfg.AppURL = appURL.String()
	}

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("landingconfig: load: %w", err)
	}

	return cfg, nil
}
