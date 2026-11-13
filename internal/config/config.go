// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package config loads calbridge's configuration from the environment
// once, at startup, and validates it fully before anything listens: see
// Load.
//
// Every value is read through govue's envconf.Loader. A secret this
// package reads will follow envconf's SecretOrFile/RequiredSecretOrFile
// contract: a direct environment value, or the same name with a "_FILE"
// suffix pointing at a file holding it, and setting both at once is an
// error rather than a silent precedence rule. Nothing here reads a secret
// yet: this milestone adds no database connection and no credential of
// its own (see the delivery order in docs/concepts/calbridge.md), so there
// is nothing to exercise that contract against today. The convention
// still governs any secret this package gains later.
package config

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
}

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

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("config: load: %w", err)
	}

	return cfg, nil
}
