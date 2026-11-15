// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Command calbridge wires the service: configuration, structured
// logging, the database pool, the sign-in providers, the HTTP handler,
// and graceful shutdown. See internal/config, internal/storage/postgres,
// internal/auth and internal/httpapi for what each piece actually does;
// this file only assembles them in order.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
	"github.com/ostendorf-cloud/calbridge/internal/config"
	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	healthRegistry := health.New()
	metricsRegistry := metrics.NewRegistry()

	// distFS is cmd/calbridge/embed.go's embedded dist/, written by
	// "task web:build"; fs.Sub strips the leading "dist" element so
	// internal/httpapi serves paths exactly as the built index.html
	// references them.
	distFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		return fmt.Errorf("calbridge: %w", err)
	}

	// The pool is opened before anything listens, so a database that
	// cannot be reached at all is a startup failure rather than a stream
	// of failed logins. ctx bounds this attempt only; the pool outlives
	// it.
	pool, err := postgres.NewPoolWithCredentials(ctx, cfg.DatabaseURL, cfg.DatabaseUser, cfg.DatabasePassword)
	if err != nil {
		// Not wrapped with the connection's own details: an error from
		// here must never carry the password that was tried.
		return fmt.Errorf("calbridge: open the database: %w", err)
	}
	defer pool.Close()

	store, err := postgres.NewStore(pool, cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("calbridge: build the store: %w", err)
	}

	providers, err := signInProviders(cfg)
	if err != nil {
		return err
	}

	handlers, err := httpapi.NewHandler(httpapi.Deps{
		Logger:              logger,
		HealthRegistry:      healthRegistry,
		MetricsRegistry:     metricsRegistry,
		DistFS:              distFS,
		AllowPlainHTTP:      cfg.SessionAllowPlainHTTP,
		RateLimitBurst:      cfg.RateLimitBurst,
		RateLimitPer:        cfg.RateLimitPer,
		GrantRateLimitBurst: cfg.GrantRateLimitBurst,
		GrantRateLimitPer:   cfg.GrantRateLimitPer,
		TrustedProxyCIDRs:   cfg.TrustedProxyCIDRs,
		Store:               store,
		AuthProviders:       providers,
	})
	if err != nil {
		return fmt.Errorf("calbridge: build http handler: %w", err)
	}

	return httpserver.RunAdmin(ctx, httpserver.Config{
		Addr:    cfg.ListenAddr,
		Handler: handlers.Public,
		Logger:  logger,
		// DrainDelay must stay above charts/calbridge's own readiness
		// probe periodSeconds times failureThreshold; see
		// config.Config.DrainDelay's own doc comment.
		DrainDelay: cfg.DrainDelay,
		// Failing the readiness probe before the drain delay starts is
		// what lets a load balancer stop sending new requests before
		// either listener stops accepting connections at all. This runs
		// once, for both listeners together; see httpserver.ServeAdmin's
		// own doc comment.
		BeforeShutdown: healthRegistry.Drain,
	}, httpserver.AdminConfig{
		Addr:    cfg.AdminListenAddr,
		Handler: handlers.Admin,
	})
}

// signInProviders builds one auth.Provider per configured sign-in
// provider. Each redirect URL is assembled here, from the public base
// URL and the route internal/httpapi actually mounts the callback at, so
// the URL registered at the provider and the route that answers it
// cannot drift apart; see httpapi.ProviderCallbackPath.
//
// A deployment that configured none gets none, which is the ordinary
// shape of a deployment offering local accounts only.
func signInProviders(cfg config.Config) ([]*auth.Provider, error) {
	providers := make([]*auth.Provider, 0, len(cfg.AuthProviders))

	for _, configured := range cfg.AuthProviders {
		provider, err := auth.NewProvider(storage.IdentityProvider(configured.ID), auth.ProviderConfig{
			Issuer:       configured.Issuer,
			ClientID:     configured.ClientID,
			ClientSecret: configured.ClientSecret,
			RedirectURL:  cfg.AuthBaseURL + httpapi.ProviderCallbackPath(configured.ID),
		})
		if err != nil {
			return nil, fmt.Errorf("calbridge: %w", err)
		}

		providers = append(providers, provider)
	}

	return providers, nil
}
