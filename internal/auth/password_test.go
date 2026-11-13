// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
)

func TestHashPasswordAndVerifyPasswordRoundTrip(t *testing.T) {
	const password = "a reasonably long passphrase"

	encoded, err := auth.HashPassword(t.Context(), password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	ok, err := auth.VerifyPassword(t.Context(), encoded, password)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}

	if !ok {
		t.Fatal("VerifyPassword() = false for the password that was hashed")
	}

	ok, err = auth.VerifyPassword(t.Context(), encoded, password+" ")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}

	if ok {
		t.Fatal("VerifyPassword() = true for a password differing by one character")
	}
}

// TestHashPasswordSaltsEveryHash: two hashes of the same password must
// not match, or one stolen hash would reveal every account using that
// password.
func TestHashPasswordSaltsEveryHash(t *testing.T) {
	first, err := auth.HashPassword(t.Context(), "the same password")
	if err != nil {
		t.Fatalf("first HashPassword() error = %v", err)
	}

	second, err := auth.HashPassword(t.Context(), "the same password")
	if err != nil {
		t.Fatalf("second HashPassword() error = %v", err)
	}

	if first == second {
		t.Fatal("two hashes of the same password are identical, want a fresh salt in each")
	}
}

// TestHashPasswordEncodesItsOwnParameters: the encoding carries the
// algorithm and its cost, which is what lets an old hash still verify
// after those constants change.
func TestHashPasswordEncodesItsOwnParameters(t *testing.T) {
	encoded, err := auth.HashPassword(t.Context(), "a reasonably long passphrase")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("encoded = %q, want the argon2id PHC encoding with this package's own parameters", encoded)
	}

	if strings.Count(encoded, "$") != 5 {
		t.Fatalf("encoded = %q, want five separators: algorithm, version, parameters, salt and hash", encoded)
	}
}

// TestVerifyPasswordStillChecksAHashWrittenUnderOtherParameters: the
// parameters come out of the stored string, not out of today's
// constants. The fixture below is an argon2id hash of "correct horse"
// computed at a deliberately lower cost than this package now uses.
func TestVerifyPasswordStillChecksAHashWrittenUnderOtherParameters(t *testing.T) {
	const (
		password = "correct horse"
		// Produced by this package's own encoding with m=8192, t=1, p=1,
		// a cost no current hash is written at.
		encoded = "$argon2id$v=19$m=8192,t=1,p=1$cGVyc2lzdGVkLXNhbHQh$" +
			"VUb1MU9keqzYdC2qxxg8TINs58T5JhM89UUu+A4En3A"
	)

	ok, err := auth.VerifyPassword(t.Context(), encoded, password)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}

	if !ok {
		t.Fatal("VerifyPassword() = false for a hash written under older parameters")
	}
}

func TestVerifyPasswordRejectsAnEncodingItDidNotWrite(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":            "",
		"not a phc string": "just-a-password",
		"another algorithm": "$argon2i$v=19$m=65536,t=3,p=4$cGVyc2lzdGVkLXNhbHQh$" +
			"Ciw0ydiL3xPgTzLemQZl3hDCsVn0zGyaBoeS5gEyXyA",
		"a version this package does not write": "$argon2id$v=16$m=65536,t=3,p=4$cGVyc2lzdGVkLXNhbHQh$" +
			"Ciw0ydiL3xPgTzLemQZl3hDCsVn0zGyaBoeS5gEyXyA",
		"salt that is not base64": "$argon2id$v=19$m=65536,t=3,p=4$not base64!$" +
			"Ciw0ydiL3xPgTzLemQZl3hDCsVn0zGyaBoeS5gEyXyA",
	}

	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ok, err := auth.VerifyPassword(t.Context(), encoded, "any password")
			if !errors.Is(err, auth.ErrPasswordHashInvalid) {
				t.Fatalf("VerifyPassword() error = %v, want ErrPasswordHashInvalid", err)
			}

			if ok {
				t.Fatal("VerifyPassword() = true for an encoding this package did not write")
			}
		})
	}
}
