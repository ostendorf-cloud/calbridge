// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestRunFailsOnInvalidConfig proves run returns the configuration error
// before it ever opens a listener: an invalid LOG_LEVEL must fail fast,
// not surface once something is already bound to a port. run itself
// needs a full environment to reach meaningfully past this point, which
// this package does not exercise in a unit test; see internal/config and
// internal/httpapi for what is actually tested there.
func TestRunFailsOnInvalidConfig(t *testing.T) {
	t.Setenv("LOG_LEVEL", "not-a-level")

	if err := run(); err == nil {
		t.Fatal("run() error = nil, want an error for an invalid LOG_LEVEL")
	}
}
