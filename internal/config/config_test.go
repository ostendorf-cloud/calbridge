// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/hex"
	"testing"
	"time"
)

// validEnv returns a getenv function satisfying every required field, so
// a test can start from a known-good baseline and change one thing. A
// key set to "" in overrides is removed from the environment instead of
// set to an empty string, so a test can exercise a missing required
// field.
func validEnv(overrides map[string]string) func(string) string {
	env := map[string]string{
		"DATABASE_URL":      "postgres://localhost:5432/calbridge?sslmode=disable",
		"DATABASE_USER":     "calbridge",
		"DATABASE_PASSWORD": "a-password",
		"ENCRYPTION_KEY":    hex.EncodeToString(make([]byte, 32)),
	}

	for k, v := range overrides {
		if v == "" {
			delete(env, k)
			continue
		}
		env[k] = v
	}

	return func(key string) string { return env[key] }
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(nil))
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
		if cfg.DatabaseUser != "calbridge" {
			t.Errorf("DatabaseUser = %q, want calbridge", cfg.DatabaseUser)
		}
		if len(cfg.EncryptionKey) != 32 {
			t.Errorf("EncryptionKey decodes to %d bytes, want 32", len(cfg.EncryptionKey))
		}
		if cfg.SessionAllowPlainHTTP {
			t.Error("SessionAllowPlainHTTP = true, want the secure default of false")
		}
		if cfg.RateLimitBurst != 10 {
			t.Errorf("RateLimitBurst = %d, want default 10", cfg.RateLimitBurst)
		}
		if cfg.RateLimitPer != time.Minute {
			t.Errorf("RateLimitPer = %v, want default 1m", cfg.RateLimitPer)
		}
	})

	t.Run("listen addr override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{"LISTEN_ADDR": ":9090"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.ListenAddr != ":9090" {
			t.Errorf("ListenAddr = %q, want :9090", cfg.ListenAddr)
		}
	})

	t.Run("admin listen addr override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{"ADMIN_LISTEN_ADDR": ":9091"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.AdminListenAddr != ":9091" {
			t.Errorf("AdminListenAddr = %q, want :9091", cfg.AdminListenAddr)
		}
	})

	t.Run("drain delay override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{"DRAIN_DELAY": "5s"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.DrainDelay != 5*time.Second {
			t.Errorf("DrainDelay = %v, want 5s", cfg.DrainDelay)
		}
	})

	t.Run("drain delay below the minimum is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"DRAIN_DELAY": "100ms"}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a DRAIN_DELAY below the minimum")
		}
	})

	t.Run("log level override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{"LOG_LEVEL": "debug"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.LogLevel.String() != "DEBUG" {
			t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
		}
	})

	t.Run("invalid log level is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"LOG_LEVEL": "not-a-level"}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for an invalid LOG_LEVEL")
		}
	})

	t.Run("missing database url is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"DATABASE_URL": ""}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a missing DATABASE_URL")
		}
	})

	t.Run("missing database user is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"DATABASE_USER": ""}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a missing DATABASE_USER")
		}
	})

	t.Run("missing database password is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"DATABASE_PASSWORD": ""}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a missing DATABASE_PASSWORD")
		}
	})

	t.Run("missing encryption key is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"ENCRYPTION_KEY": ""}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a missing ENCRYPTION_KEY")
		}
	})

	t.Run("encryption key too short", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"ENCRYPTION_KEY": hex.EncodeToString(make([]byte, 16))}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for an encryption key under 32 bytes")
		}
	})

	t.Run("encryption key too long", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"ENCRYPTION_KEY": hex.EncodeToString(make([]byte, 64))}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for an encryption key over 32 bytes")
		}
	})

	t.Run("encryption key not hex", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"ENCRYPTION_KEY": "not-hex!"}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a non-hex ENCRYPTION_KEY")
		}
	})

	t.Run("session allow plain http override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{"SESSION_ALLOW_PLAIN_HTTP": "true"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !cfg.SessionAllowPlainHTTP {
			t.Error("SessionAllowPlainHTTP = false, want true")
		}
	})

	t.Run("rate limit override", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{"RATE_LIMIT_BURST": "5", "RATE_LIMIT_PER": "30s"}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.RateLimitBurst != 5 {
			t.Errorf("RateLimitBurst = %d, want 5", cfg.RateLimitBurst)
		}
		if cfg.RateLimitPer != 30*time.Second {
			t.Errorf("RateLimitPer = %v, want 30s", cfg.RateLimitPer)
		}
	})

	t.Run("rate limit per below the minimum is an error", func(t *testing.T) {
		t.Parallel()

		_, err := Load(validEnv(map[string]string{"RATE_LIMIT_PER": "100ms"}))
		if err == nil {
			t.Fatal("Load() error = nil, want an error for a RATE_LIMIT_PER below the minimum")
		}
	})
}

// TestLoadWithNilGetenvReadsTheRealProcessEnvironment is its own top
// level test, not a TestLoad subtest: t.Setenv forbids a parallel
// ancestor, and TestLoad's own subtests above all call t.Parallel(). It
// sets every required variable itself, through t.Setenv, rather than
// relying on validEnv's map, since Load(nil) reads the real process
// environment, not a map.
func TestLoadWithNilGetenvReadsTheRealProcessEnvironment(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/calbridge?sslmode=disable")
	t.Setenv("DATABASE_USER", "calbridge")
	t.Setenv("DATABASE_PASSWORD", "a-password")
	t.Setenv("ENCRYPTION_KEY", hex.EncodeToString(make([]byte, 32)))

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want default :8080", cfg.ListenAddr)
	}
}
