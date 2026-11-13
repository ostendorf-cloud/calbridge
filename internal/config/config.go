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
	"os"
	"time"

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

	cfg.DatabaseURL = l.Required("DATABASE_URL")
	cfg.DatabaseUser = l.Required("DATABASE_USER")
	cfg.DatabasePassword = l.RequiredSecretOrFile("DATABASE_PASSWORD")

	encryptionKey, err := decodeHexKey("ENCRYPTION_KEY", l.SecretOrFile("ENCRYPTION_KEY"), encryptionKeyBytes)
	fail(err)
	cfg.EncryptionKey = encryptionKey

	if err := errors.Join(append(problems, l.Err())...); err != nil {
		return Config{}, fmt.Errorf("config: load: %w", err)
	}

	return cfg, nil
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
		return nil, fmt.Errorf("%s: must be hex encoded: %w", name, err)
	}

	if len(key) != wantBytes {
		return nil, fmt.Errorf("%s: decodes to %d bytes, exactly %d are required", name, len(key), wantBytes)
	}

	return key, nil
}
