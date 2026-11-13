// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth

// ScopesFor is what a sign-in provider was actually configured to ask
// for. It is test-only on purpose: the scope list is fixed by this
// package and nothing outside it has a reason to read one, but the test
// that holds the consent split down has to assert on what NewProvider
// built rather than on the constant it read from.
func ScopesFor(p *Provider) []string {
	return p.scopes
}
