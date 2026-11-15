// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

// Command calbridge-landing serves the public landing page on
// calbridge.de: what calbridge does, pricing, the imprint and the
// privacy policy, and the call to action pointing at app.calbridge.de.
// See internal/landingconfig and internal/landingapi for what each piece
// does; this file only assembles them in order, the same shape
// cmd/calbridge's own main uses for the application.
//
// This binary opens no database connection and keeps no session: the
// static build it serves is read-only, so a correction to the privacy
// policy's text is a new image for this binary alone, never a
// redeployment of the application.
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

	"github.com/ostendorf-cloud/calbridge/internal/landingapi"
	"github.com/ostendorf-cloud/calbridge/internal/landingconfig"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := landingconfig.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	healthRegistry := health.New()
	metricsRegistry := metrics.NewRegistry()

	// distFS is embed.go's embedded dist/, written by "task
	// landing:build"; fs.Sub strips the leading "dist" element so
	// internal/landingapi serves paths exactly as the built index.html
	// references them, matching cmd/calbridge's own main.
	siteFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		return fmt.Errorf("calbridge-landing: %w", err)
	}

	handlers, err := landingapi.NewHandler(landingapi.Deps{
		Logger:          logger,
		HealthRegistry:  healthRegistry,
		MetricsRegistry: metricsRegistry,
		DistFS:          siteFS,
		AppURL:          cfg.AppURL,
	})
	if err != nil {
		return fmt.Errorf("calbridge-landing: build http handler: %w", err)
	}

	return httpserver.RunAdmin(ctx, httpserver.Config{
		Addr:       cfg.ListenAddr,
		Handler:    handlers.Public,
		Logger:     logger,
		DrainDelay: cfg.DrainDelay,
		// Failing the readiness probe before the drain delay starts lets
		// a load balancer stop sending new requests before either
		// listener stops accepting connections; see health.Registry.Drain.
		BeforeShutdown: healthRegistry.Drain,
	}, httpserver.AdminConfig{
		Addr:    cfg.AdminListenAddr,
		Handler: handlers.Admin,
	})
}
