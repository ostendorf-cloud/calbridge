// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingconfig

import (
	"strings"
	"testing"
	"time"
)

// validEnv returns a getenv function satisfying every required field,
// matching internal/config's own validEnv helper: a test starts from a
// known-good baseline and changes one thing. A key set to "" in
// overrides is removed from the environment instead of set to an empty
// string, so a test can exercise a missing required field.
func validEnv(overrides map[string]string) func(string) string {
	env := map[string]string{
		"APP_URL":              "https://app.calbridge.example",
		"OPERATOR_NAME":        "Erika Mustermann",
		"OPERATOR_STREET":      "Musterstrasse 12",
		"OPERATOR_POSTAL_CODE": "10115",
		"OPERATOR_CITY":        "Berlin",
		"OPERATOR_EMAIL":       "kontakt@calbridge.example",
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

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := Load(validEnv(nil))
	if err != nil {
		t.Fatalf("Load = %v, want no error", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.AdminListenAddr != ":8081" {
		t.Errorf("AdminListenAddr = %q, want %q", cfg.AdminListenAddr, ":8081")
	}
	if cfg.AppURL != "https://app.calbridge.example" {
		t.Errorf("AppURL = %q, want %q", cfg.AppURL, "https://app.calbridge.example")
	}
}

// TestLoadDropsATrailingSlashFromAppURL is one half of the direction the
// two validators agree on; internal/config's own
// TestCheckPublicSiteURLAcceptsATrailingSlash is the other. A URL whose
// path is only a slash is accepted on both sides and normalised away,
// because envconf's Loader.URL, which this repository does not own,
// already does exactly that, and a value the landing server starts on
// must not be one the application refuses.
func TestLoadDropsATrailingSlashFromAppURL(t *testing.T) {
	t.Parallel()

	cfg, err := Load(validEnv(map[string]string{"APP_URL": "https://app.calbridge.example/"}))
	if err != nil {
		t.Fatalf("Load = %v, want no error", err)
	}

	if cfg.AppURL != "https://app.calbridge.example" {
		t.Errorf("AppURL = %q, want the trailing slash dropped", cfg.AppURL)
	}
}

func TestLoadRejectsAMissingAppURL(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"APP_URL": ""}))
	if err == nil {
		t.Fatal("Load accepted a config with no APP_URL")
	}
	if !strings.Contains(err.Error(), "APP_URL") {
		t.Errorf("error %q does not mention APP_URL", err)
	}
}

func TestLoadRejectsANonURLAppURL(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"APP_URL": "app.calbridge.example"}))
	if err == nil {
		t.Fatal("Load accepted an AppURL with no scheme")
	}
}

// TestLoadRejectsAnAppURLWithCredentials states on this side what
// internal/config's own checkPublicSiteURL states on the other: the two
// hosts link to each other, so they have to agree on what a usable base
// URL is. envconf's Loader.URL refuses the complete shape; the value
// that forgot its scheme is refused by this package, because url.Parse
// reads "user" as the scheme there and envconf then reports a missing
// scheme while quoting the password.
func TestLoadRejectsAnAppURLWithCredentials(t *testing.T) {
	t.Parallel()

	const password = "not-for-a-log-line"

	values := map[string]string{
		"with a scheme":    "https://user:" + password + "@app.calbridge.example",
		"with no scheme":   "user:" + password + "@app.calbridge.example",
		"with a path, too": "user:" + password + "@app.calbridge.example/pricing",
	}

	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(validEnv(map[string]string{"APP_URL": value}))
			if err == nil {
				t.Fatal("Load accepted an AppURL carrying credentials")
			}
			if !strings.Contains(err.Error(), "must not contain credentials") {
				t.Errorf("error %q does not name the credentials as the problem", err)
			}
			if strings.Contains(err.Error(), password) {
				t.Errorf("error %q repeats the password out of the URL", err)
			}
		})
	}
}

// TestNamesCredentialsBeforeHost covers the shapes the credentials check
// has to tell apart. internal/config holds the same table for its own
// copy of this function, and the two have to stay identical.
func TestNamesCredentialsBeforeHost(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"https://app.calbridge.example":         false,
		"https://app.calbridge.example/pricing": false,
		"app.calbridge.example":                 false,
		"":                                      false,
		"https://app.calbridge.example/mail@example.com": false,
		"https://user:secret@app.calbridge.example":      true,
		"user:secret@app.calbridge.example":              true,
		"user@app.calbridge.example":                     true,
		"  user:secret@app.calbridge.example  ":          true,
		"http://user:secret@app.calbridge.example/x":     true,
	}

	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			if got := namesCredentialsBeforeHost(value); got != want {
				t.Errorf("namesCredentialsBeforeHost(%q) = %v, want %v", value, got, want)
			}
		})
	}
}

func TestLoadRejectsAnUnknownLogLevel(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"LOG_LEVEL": "verbose"}))
	if err == nil {
		t.Fatal("Load accepted an unknown log level")
	}
}

func TestLoadReadsADrainDelay(t *testing.T) {
	t.Parallel()

	cfg, err := Load(validEnv(map[string]string{"DRAIN_DELAY": "7s"}))
	if err != nil {
		t.Fatalf("Load = %v, want no error", err)
	}
	if cfg.DrainDelay != 7*time.Second {
		t.Errorf("DrainDelay = %v, want 7s", cfg.DrainDelay)
	}
}
