// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 and RFC 4226's specified HMAC hash; used as a MAC, not for collision resistance.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// TOTP (RFC 6238, built on the HOTP of RFC 4226) is implemented directly
// against the standard library here rather than through a third party
// module. That was not the first choice: go-conventions prefers a
// well-maintained library over new crypto code, and one was looked for
// first. This repository's module proxy cannot fetch an uncached module
// (every attempt against github.com/pquerna/otp, a well-known, actively
// maintained choice, answers "tls: failed to verify certificate", while
// the already-cached golang.org/x/crypto and
// github.com/ostendorf-cloud/govue resolve), so this is a documented
// fallback, not a preference: the algorithm is small, fully specified by
// RFC 6238 and RFC 4226, and its SHA-1 HMAC core is correctness-tested
// against RFC 6238 appendix B's own published vectors. solobill's
// internal/auth reached the same conclusion against the same proxy and
// carries the same implementation.
//
// totpDigits, totpStep and totpSkewSteps are Google Authenticator's and
// most other TOTP apps' de facto defaults: 6 digits, a 30 second step,
// and the code one step either side of now accepted as well, so a code
// typed a few seconds late because of input delay or modest clock drift
// between the server and the device is not rejected.
const (
	totpDigits    = 6
	totpStep      = 30 * time.Second
	totpSkewSteps = 1
	// totpSecretBytes is 160 bits, RFC 4226's own recommended HMAC-SHA-1
	// key size (section 4, R6): "the secret length MUST be at least 128
	// bits... it is RECOMMENDED to use a secret length of 160 bits." It
	// is also what makes the base32 encoding below exactly 32 characters
	// long, which is what users.totp_secret's own CHECK constraint
	// requires (see db/migrations).
	totpSecretBytes = 20
)

// TOTPIssuer is the issuer name an authenticator app groups calbridge's
// entries under, and the prefix of the account label inside it.
const TOTPIssuer = "calbridge"

// base32Encoding is RFC 4648 base32, no padding: the shape every TOTP
// app expects a secret to be shown or scanned in, and the shape
// users.totp_secret is documented to hold (see db/migrations).
var base32Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh random secret, base32 encoded the
// way an authenticator app and users.totp_secret both expect it, ready
// to pass to TOTPProvisioningURI for the person to scan and later to
// VerifyTOTPCode.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, totpSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: read totp secret: %w", err)
	}

	return base32Encoding.EncodeToString(buf), nil
}

// TOTPProvisioningURI builds the otpauth://totp/... URI an authenticator
// app reads from a QR code, per the de facto "Key URI Format" every
// major TOTP app follows (originally documented by Google
// Authenticator; there is no RFC for it). Rendering a QR code from it is
// the UI's concern, not this package's.
//
// accountName identifies the account inside the app, conventionally the
// email the account was created under. It is checked here rather than
// assumed: this function is exported, and every caller having happened
// to validate its input already is not a property the function itself
// carries. An empty name, one longer than maxTOTPAccountName, or one
// holding a control character is refused, since none of those is a label
// an authenticator app can show and the last would mean the caller never
// checked its input at all.
func TOTPProvisioningURI(secret, accountName string) (string, error) {
	switch {
	case accountName == "":
		return "", errors.New("auth: totp account name is empty")
	case len(accountName) > maxTOTPAccountName:
		return "", fmt.Errorf("auth: totp account name is longer than %d bytes", maxTOTPAccountName)
	}

	for _, r := range accountName {
		if unicode.IsControl(r) {
			return "", errors.New("auth: totp account name holds a control character")
		}
	}

	u := url.URL{
		Scheme: "otpauth",
		Host:   "totp",
		Path:   "/" + TOTPIssuer + ":" + accountName,
	}

	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", TOTPIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", int(totpStep.Seconds())))
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// maxTOTPAccountName bounds the label inside the provisioning URI. It is
// the longest address RFC 5321 section 4.5.3.1.3 allows, which is the
// only kind of name calbridge puts here, so nothing legitimate is cut
// off and a QR code stays a size a phone camera can read.
const maxTOTPAccountName = 254

// GenerateTOTPCode computes the code for secret at the step containing
// at. It exists for a test that wants to check a just-generated secret
// round trips without a fixture carrying a known vector.
func GenerateTOTPCode(secret string, at time.Time) (string, error) {
	key, err := base32Encoding.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("auth: totp secret is not base32: %w", err)
	}

	return hotp(key, counterAt(at, 0), totpDigits), nil
}

// VerifyTOTPCode reports whether code is valid for secret at time at,
// accepting the step containing at and totpSkewSteps steps either side
// of it, and if so, the RFC 6238 counter the matched step used: a caller
// compares that counter against the account's last accepted one (see
// storage.User.TOTPLastCounter) and refuses a code whose counter is not
// strictly greater, so the exact same code cannot be replayed (RFC 6238
// section 5.2). counter is 0 and meaningless when ok is false. at is
// explicit rather than defaulted to time.Now internally, the same choice
// session.Config and oidc.Config make for their own clocks.
//
// A secret that is not valid base32 is treated as "never matches" rather
// than as an error: Local.Login compares against a dummy secret for an
// account that does not exist at all, and that path must stay
// indistinguishable from a wrong code, not surface a decode error.
func VerifyTOTPCode(secret, code string, at time.Time) (counter uint64, ok bool) {
	key, err := base32Encoding.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}

	// Every candidate step is checked, not short-circuited on the first
	// match: the number of steps compared must not depend on which step,
	// if any, matches, since that is exactly the kind of timing signal a
	// second factor must not leak.
	matched := 0

	var matchedCounter uint64

	for offset := -totpSkewSteps; offset <= totpSkewSteps; offset++ {
		candidateCounter := counterAt(at, offset)
		candidate := hotp(key, candidateCounter, totpDigits)

		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			matched = 1
			matchedCounter = candidateCounter
		}
	}

	return matchedCounter, matched == 1
}

// counterAt is the RFC 6238 time counter: the number of whole totpStep
// periods since the Unix epoch, offset by a number of steps either side
// for the skew window.
func counterAt(at time.Time, offsetSteps int) uint64 {
	step := int64(totpStep.Seconds())

	counter := at.Unix()/step + int64(offsetSteps)
	if counter < 0 {
		counter = 0
	}

	return uint64(counter)
}

// hotp is RFC 4226's HOTP algorithm: an HMAC-SHA-1 of the 8 byte big
// endian counter, dynamically truncated to digits decimal digits
// (sections 5.3 and 5.4). digits must be small enough that 10^digits
// fits an int32, true for every digit count any TOTP app uses (6 or 8).
func hotp(key []byte, counter uint64, digits int) string {
	var counterBytes [8]byte

	binary.BigEndian.PutUint64(counterBytes[:], counter)

	mac := hmac.New(sha1.New, key)
	// hash.Hash.Write never returns an error.
	_, _ = mac.Write(counterBytes[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])

	mod := uint32(1)
	for range digits {
		mod *= 10
	}

	return fmt.Sprintf("%0*d", digits, truncated%mod)
}
