// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package httpapi

// LoginError and SignUpError are the two places this package decides
// what an internal/auth refusal is answered with. They are exported to
// a test only: reaching the shed path through a real request would mean
// holding every one of internal/auth's derivation slots for the length
// of the request, which is a race to arrange and nothing to do with what
// the mapping has to get right.
var (
	LoginError  = loginError
	SignUpError = signUpError
)
