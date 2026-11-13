// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

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

	t.Run("nil getenv reads the real process environment", func(t *testing.T) {
		t.Parallel()

		if _, err := Load(nil); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})
}
