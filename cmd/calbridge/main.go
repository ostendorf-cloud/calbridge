// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Command calbridge wires the service: configuration, structured
// logging, the HTTP handler, and graceful shutdown. See internal/config
// and internal/httpapi for what each piece actually does; this file only
// assembles them in order.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ostendorf-cloud/govue/health"
	"github.com/ostendorf-cloud/govue/httpserver"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/metrics"

	"github.com/ostendorf-cloud/calbridge/internal/config"
	"github.com/ostendorf-cloud/calbridge/internal/httpapi"
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

	handlers, err := httpapi.NewHandler(httpapi.Deps{
		Logger:          logger,
		HealthRegistry:  healthRegistry,
		MetricsRegistry: metricsRegistry,
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
