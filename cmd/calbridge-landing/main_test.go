// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package main

import "testing"

// TestRunFailsOnInvalidConfig proves run returns the configuration error
// before it ever opens a listener, matching cmd/calbridge's own test:
// an invalid LOG_LEVEL must fail fast, not surface once something is
// already bound to a port.
func TestRunFailsOnInvalidConfig(t *testing.T) {
	t.Setenv("LOG_LEVEL", "not-a-level")

	if err := run(); err == nil {
		t.Fatal("run() error = nil, want an error for an invalid LOG_LEVEL")
	}
}

// TestRunFailsOnAMissingAppURL proves the one setting this binary
// cannot default is checked before anything listens: a landing page
// with nowhere to point its call to action is a deployment mistake to
// fail loudly on at startup.
func TestRunFailsOnAMissingAppURL(t *testing.T) {
	t.Setenv("APP_URL", "")

	if err := run(); err == nil {
		t.Fatal("run() error = nil, want an error for a missing APP_URL")
	}
}
