// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth_test

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
)

// rfc6238Secret is the HMAC-SHA-1 key RFC 6238 appendix B's test vectors
// are computed with: the ASCII string "12345678901234567890", given in
// the RFC as the seed 0x3132...3930. This package takes a base32 secret,
// the shape an authenticator app and users.totp_secret both use, so the
// same 20 bytes are spelled that way here.
var rfc6238Secret = base32.StdEncoding.WithPadding(base32.NoPadding).
	EncodeToString([]byte("12345678901234567890"))

// TestGenerateTOTPCodeMatchesRFC6238TestVectors checks the HOTP core
// against the published vectors rather than against itself. The RFC
// tabulates 8 digit codes; this package issues 6, which are the last six
// digits of the same truncation, so each expectation below is the RFC's
// own value with its leading two digits dropped.
func TestGenerateTOTPCodeMatchesRFC6238TestVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		unixTime int64
		want     string
	}{
		{59, "287082"},          // RFC 6238 appendix B, SHA1 row: 94287082
		{1111111109, "081804"},  // 07081804
		{1111111111, "050471"},  // 14050471
		{1234567890, "005924"},  // 89005924
		{2000000000, "279037"},  // 69279037
		{20000000000, "353130"}, // 65353130
	}

	for _, test := range tests {
		t.Run(time.Unix(test.unixTime, 0).UTC().Format(time.RFC3339), func(t *testing.T) {
			t.Parallel()

			got, err := auth.GenerateTOTPCode(rfc6238Secret, time.Unix(test.unixTime, 0).UTC())
			if err != nil {
				t.Fatalf("GenerateTOTPCode() error = %v", err)
			}

			if got != test.want {
				t.Fatalf("GenerateTOTPCode() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestGenerateTOTPSecretFitsTheColumnAndTheApps: 20 random bytes in RFC
// 4648 base32 with no padding is 32 characters, which is exactly what
// users.totp_secret's CHECK constraint accepts.
func TestGenerateTOTPSecretFitsTheColumnAndTheApps(t *testing.T) {
	t.Parallel()

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	if len(secret) != 32 {
		t.Fatalf("len(secret) = %d, want 32, the length users.totp_secret requires", len(secret))
	}

	if strings.ContainsAny(secret, "=018") {
		t.Fatalf("secret = %q, want unpadded RFC 4648 base32, whose alphabet excludes these", secret)
	}

	second, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("second GenerateTOTPSecret() error = %v", err)
	}

	if secret == second {
		t.Fatal("two generated secrets are identical, want a fresh one each time")
	}
}

func TestVerifyTOTPCodeAcceptsTheCurrentStepAndOneEitherSide(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		codeAt     time.Time
		verifyAt   time.Time
		wantAccept bool
	}{
		"the current step":    {now, now, true},
		"one step behind":     {now.Add(-30 * time.Second), now, true},
		"one step ahead":      {now.Add(30 * time.Second), now, true},
		"two steps behind":    {now.Add(-60 * time.Second), now, false},
		"two steps ahead":     {now.Add(60 * time.Second), now, false},
		"an hour out of date": {now.Add(-time.Hour), now, false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			code, err := auth.GenerateTOTPCode(rfc6238Secret, test.codeAt)
			if err != nil {
				t.Fatalf("GenerateTOTPCode() error = %v", err)
			}

			_, ok := auth.VerifyTOTPCode(rfc6238Secret, code, test.verifyAt)
			if ok != test.wantAccept {
				t.Fatalf("VerifyTOTPCode() = %t, want %t", ok, test.wantAccept)
			}
		})
	}
}

// TestVerifyTOTPCodeReportsTheMatchedCounter: the counter is what lets a
// caller refuse a replay of the same code, so it has to name the step
// that actually matched.
func TestVerifyTOTPCodeReportsTheMatchedCounter(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

	code, err := auth.GenerateTOTPCode(rfc6238Secret, at)
	if err != nil {
		t.Fatalf("GenerateTOTPCode() error = %v", err)
	}

	counter, ok := auth.VerifyTOTPCode(rfc6238Secret, code, at)
	if !ok {
		t.Fatal("VerifyTOTPCode() = false for a code generated at the same instant")
	}

	if want := uint64(at.Unix() / 30); counter != want {
		t.Fatalf("counter = %d, want %d, the RFC 6238 step containing this instant", counter, want)
	}
}

// TestVerifyTOTPCodeTreatsAnUnusableSecretAsNeverMatching: Local.Login
// compares against a dummy secret for an account that does not exist, so
// a secret that cannot be decoded must look exactly like a wrong code,
// not like an error.
func TestVerifyTOTPCodeTreatsAnUnusableSecretAsNeverMatching(t *testing.T) {
	t.Parallel()

	for _, secret := range []string{"", "not base32!", "1111"} {
		if _, ok := auth.VerifyTOTPCode(secret, "123456", time.Now()); ok {
			t.Fatalf("VerifyTOTPCode(%q, ...) = true, want false", secret)
		}
	}
}

func TestVerifyTOTPCodeRejectsAnEmptyCode(t *testing.T) {
	t.Parallel()

	if _, ok := auth.VerifyTOTPCode(rfc6238Secret, "", time.Now()); ok {
		t.Fatal("VerifyTOTPCode() = true for an empty code")
	}
}

// TestTOTPProvisioningURIIsWhatAnAuthenticatorAppExpects checks the de
// facto Key URI Format: the otpauth scheme, the totp type, the issuer
// both in the label and as a parameter, and the algorithm, digits and
// period this package actually uses.
func TestTOTPProvisioningURIIsWhatAnAuthenticatorAppExpects(t *testing.T) {
	t.Parallel()

	const (
		secret  = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
		account = "person@example.com"
	)

	raw := auth.TOTPProvisioningURI(secret, account)

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse the provisioning uri %q: %v", raw, err)
	}

	if parsed.Scheme != "otpauth" || parsed.Host != "totp" {
		t.Fatalf("uri = %q, want an otpauth://totp/... uri", raw)
	}

	if want := "/" + auth.TOTPIssuer + ":" + account; parsed.Path != want {
		t.Fatalf("path = %q, want %q", parsed.Path, want)
	}

	query := parsed.Query()

	wantParams := map[string]string{
		"secret":    secret,
		"issuer":    auth.TOTPIssuer,
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
	}

	for key, want := range wantParams {
		if got := query.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
