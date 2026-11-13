// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package config

import (
	"testing"
	"time"
)

// env returns a getenv function backed by m, so a test can start from a
// known map and change one entry.
func env(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("defaults with no environment set", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(env(nil))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.ListenAddr != ":8080" {
			t.Errorf("ListenAddr = %q, want default :8080", cfg.ListenAddr)
		}
		if cfg.AdminListenAddr != ":8081" {
			t.Errorf("AdminListenAddr = %q, want default :8081", cfg.AdminListenAddr)
		}
		if cfg.DrainDelay != 2*time.Second {
			t.Errorf("DrainDelay = %v, want default 2s", cfg.DrainDelay)
		}
		if cfg.LogLevel.String() != "INFO" {
			t.Errorf("LogLevel = %v, want default info", cfg.LogLevel)
		}
	})

	t.Run("listen addr override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(env(map[string]string{"LISTEN_ADDR": ":9090"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.ListenAddr != ":9090" {
			t.Errorf("ListenAddr = %q, want :9090", cfg.ListenAddr)
		}
	})

	t.Run("admin listen addr override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(env(map[string]string{"ADMIN_LISTEN_ADDR": ":9091"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AdminListenAddr != ":9091" {
			t.Errorf("AdminListenAddr = %q, want :9091", cfg.AdminListenAddr)
		}
	})

	t.Run("drain delay override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(env(map[string]string{"DRAIN_DELAY": "5s"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.DrainDelay != 5*time.Second {
			t.Errorf("DrainDelay = %v, want 5s", cfg.DrainDelay)
		}
	})

	t.Run("drain delay below the minimum is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(env(map[string]string{"DRAIN_DELAY": "100ms"}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a DRAIN_DELAY below the minimum")
		}
	})

	t.Run("log level override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(env(map[string]string{"LOG_LEVEL": "debug"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.LogLevel.String() != "DEBUG" {
			t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
		}
	})

	t.Run("invalid log level is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(env(map[string]string{"LOG_LEVEL": "not-a-level"}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for an invalid LOG_LEVEL")
		}
	})
}

// TestLoadWithNilGetenvReadsTheRealProcessEnvironment is its own top
// level test, not a TestLoad subtest: t.Setenv forbids a parallel
// ancestor, and TestLoad's own subtests above all call t.Parallel(). It
// pins the two keys a runner's own ambient environment could otherwise
// set, for example a LOG_LEVEL left exported by the shell that started
// this test run, rather than asserting only that Load(nil) returns no
// error regardless of what that environment happens to contain.
func TestLoadWithNilGetenvReadsTheRealProcessEnvironment(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want default :8080", cfg.ListenAddr)
	}
}
