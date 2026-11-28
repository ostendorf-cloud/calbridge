// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package main

import (
	"strings"
	"testing"
)

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

// TestRunRefusesAnOperatorStillAPlaceholder proves the production landing
// is never served with a placeholder in its imprint: the refusal comes
// from the configuration, before anything listens.
func TestRunRefusesAnOperatorStillAPlaceholder(t *testing.T) {
	t.Setenv("APP_URL", "https://app.calbridge.example")
	t.Setenv("OPERATOR_NAME", "Erika Mustermann")
	t.Setenv("OPERATOR_STREET", "[Postal address, filled in by the owner]")
	t.Setenv("OPERATOR_POSTAL_CODE", "10115")
	t.Setenv("OPERATOR_CITY", "Berlin")
	t.Setenv("OPERATOR_EMAIL", "kontakt@calbridge.example")

	err := run()
	if err == nil {
		t.Fatal("run() error = nil, want an error for a placeholder operator field")
	}
	if !strings.Contains(err.Error(), "OPERATOR_STREET") {
		t.Errorf("run() error = %q, want it to name OPERATOR_STREET", err)
	}
}

// TestRunRefusesAMissingOperator covers an empty environment for the
// operator: the imprint must not be served empty either.
func TestRunRefusesAMissingOperator(t *testing.T) {
	t.Setenv("APP_URL", "https://app.calbridge.example")
	for _, key := range []string{"OPERATOR_NAME", "OPERATOR_STREET", "OPERATOR_POSTAL_CODE", "OPERATOR_CITY", "OPERATOR_EMAIL"} {
		t.Setenv(key, "")
	}

	err := run()
	if err == nil {
		t.Fatal("run() error = nil, want an error for a missing operator")
	}
	if !strings.Contains(err.Error(), "OPERATOR_NAME") {
		t.Errorf("run() error = %q, want it to name OPERATOR_NAME", err)
	}
}
