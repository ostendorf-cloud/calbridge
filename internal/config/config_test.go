// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
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

	t.Run("no sign-in provider is configured by default", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(nil))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(cfg.AuthProviders) != 0 {
			t.Errorf("AuthProviders = %v, want none by default", cfg.AuthProviders)
		}
		if cfg.AuthBaseURL != "" {
			t.Errorf("AuthBaseURL = %q, want empty by default", cfg.AuthBaseURL)
		}
	})

	t.Run("a configured provider is loaded in the fixed order", func(t *testing.T) {
		t.Parallel()

		cfg, err := Load(validEnv(map[string]string{
			"AUTH_BASE_URL":                "https://calbridge.example.com",
			"AUTH_APPLE_CLIENT_ID":         "an-apple-client",
			"AUTH_APPLE_CLIENT_SECRET":     "an-apple-secret",
			"AUTH_GOOGLE_CLIENT_ID":        "a-google-client",
			"AUTH_GOOGLE_CLIENT_SECRET":    "a-google-secret",
			"AUTH_MICROSOFT_CLIENT_ID":     "a-microsoft-client",
			"AUTH_MICROSOFT_CLIENT_SECRET": "a-microsoft-secret",
			"AUTH_MICROSOFT_ISSUER":        "https://login.microsoftonline.com/a-tenant/v2.0",
		}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if len(cfg.AuthProviders) != 3 {
			t.Fatalf("AuthProviders = %d, want 3", len(cfg.AuthProviders))
		}

		wantOrder := []string{"google", "microsoft", "apple"}
		for i, want := range wantOrder {
			if got := cfg.AuthProviders[i].ID; got != want {
				t.Errorf("AuthProviders[%d].ID = %q, want %q: the order must not change between restarts", i, got, want)
			}
		}

		if got := cfg.AuthProviders[1].Issuer; got != "https://login.microsoftonline.com/a-tenant/v2.0" {
			t.Errorf("microsoft Issuer = %q, want the tenant-specific one it was given", got)
		}

		if got := cfg.AuthProviders[0].Issuer; got != "" {
			t.Errorf("google Issuer = %q, want empty so internal/auth's own default applies", got)
		}
	})

	t.Run("a client secret may come from a file", func(t *testing.T) {
		t.Parallel()

		secretFile := filepath.Join(t.TempDir(), "google-client-secret")
		if err := os.WriteFile(secretFile, []byte("a-google-secret\n"), 0o600); err != nil {
			t.Fatalf("write the secret file: %v", err)
		}

		cfg, err := Load(validEnv(map[string]string{
			"AUTH_BASE_URL":                  "https://calbridge.example.com",
			"AUTH_GOOGLE_CLIENT_ID":          "a-google-client",
			"AUTH_GOOGLE_CLIENT_SECRET_FILE": secretFile,
		}))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if len(cfg.AuthProviders) != 1 || cfg.AuthProviders[0].ClientSecret != "a-google-secret" {
			t.Fatalf("AuthProviders = %+v, want one google provider whose secret came from the file", cfg.AuthProviders)
		}
	})

	t.Run("provider configuration refusals", func(t *testing.T) {
		t.Parallel()

		tests := map[string]map[string]string{
			"a client id with no secret": {
				"AUTH_BASE_URL":         "https://calbridge.example.com",
				"AUTH_GOOGLE_CLIENT_ID": "a-google-client",
			},
			"a secret with no client id": {
				"AUTH_BASE_URL":             "https://calbridge.example.com",
				"AUTH_GOOGLE_CLIENT_SECRET": "a-google-secret",
			},
			"a provider with no base url": {
				"AUTH_GOOGLE_CLIENT_ID":     "a-google-client",
				"AUTH_GOOGLE_CLIENT_SECRET": "a-google-secret",
			},
			"a base url that is not absolute": {
				"AUTH_BASE_URL":             "calbridge.example.com",
				"AUTH_GOOGLE_CLIENT_ID":     "a-google-client",
				"AUTH_GOOGLE_CLIENT_SECRET": "a-google-secret",
			},
			"a base url carrying a path": {
				"AUTH_BASE_URL":             "https://calbridge.example.com/app",
				"AUTH_GOOGLE_CLIENT_ID":     "a-google-client",
				"AUTH_GOOGLE_CLIENT_SECRET": "a-google-secret",
			},
		}

		for name, overrides := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := Load(validEnv(overrides))
				if err == nil {
					t.Fatal("Load() error = nil, want a refusal")
				}

				if strings.Contains(err.Error(), "a-google-secret") {
					t.Fatalf("Load() error = %v, want it never to carry a client secret's value", err)
				}
			})
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
