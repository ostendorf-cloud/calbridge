// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth

import "testing"

// ScopesFor is what a sign-in provider was actually configured to ask
// for. It is test-only on purpose: the scope list is fixed by this
// package and nothing outside it has a reason to read one, but the test
// that holds the consent split down has to assert on what NewProvider
// built rather than on the constant it read from.
func ScopesFor(p *Provider) []string {
	return p.scopes
}

// SetDeriveKey replaces the Argon2id derivation with observe and
// restores the real one when the test ends. It exists so a test can
// watch how many derivations run at once without this package counting
// them in production code, and without waiting for real derivations.
//
// A test using it must not call t.Parallel: derivation is package state,
// and a parallel test calling HashPassword or VerifyPassword would race
// with the swap. Go runs the non-parallel tests of a binary while every
// parallel one is still paused, so a test that simply does not opt in is
// safe.
func SetDeriveKey(t *testing.T, observe func(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte) {
	t.Helper()

	previous := derivation
	derivation = observe

	t.Cleanup(func() { derivation = previous })
}

// MaxHashWait is how long a caller waits for a derivation slot, for a
// test that needs to know when shedding starts.
const MaxHashWait = maxHashWait
