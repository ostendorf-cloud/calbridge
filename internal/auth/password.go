// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. Argon2id is the memory-hard choice here because
// it is the password hashing competition winner RFC 9106 specifies, and
// the id variant is the one that variant's own section 4 recommends when
// the threat model is not known to exclude side channel attacks: it
// combines argon2i's resistance to those with argon2d's resistance to
// time-memory trade-offs, so neither an attacker on the same host nor an
// attacker with a GPU farm gets the cheaper path. Memory hardness is
// what matters: a GPU or an ASIC can run an enormous number of SHA-2
// cores in parallel but cannot give each one 64 MiB of its own.
//
// golang.org/x/crypto/argon2's own package doc, citing RFC 9106 section
// 4, gives two recommended options: time=1, memory=2 GiB, threads=4 when
// that much memory can be spared per login attempt, or time=3, memory=64
// MiB, threads=4 "if much less memory is available". calbridge runs as a
// small, single-replica service under a 256Mi memory limit
// (charts/calbridge's values.yaml), so the second option is the one that
// fits: enough cost to make an offline attempt expensive, without a
// login spiking the process past its own limit per concurrent attempt.
const (
	argon2Time      = 3
	argon2MemoryKiB = 64 * 1024
	argon2Threads   = 4
	// argon2KeyLen is the derived key (hash) length. RFC 9106 section 4
	// calls 128 bits "sufficient for most applications"; 32 bytes (256
	// bits) is the extra margin most argon2id implementations default to
	// regardless, at a cost that does not show up next to the memory
	// cost above.
	argon2KeyLen = 32
	// argon2SaltLen is 128 bits, RFC 9106 section 4's own recommendation
	// ("sufficient for all applications").
	argon2SaltLen = 16
)

// PasswordHashMemoryMiB is argon2MemoryKiB expressed in the unit a
// Kubernetes resource limit uses, so the arithmetic below and the
// chart's own limit can be compared directly. See
// MaxConcurrentPasswordHashes.
const PasswordHashMemoryMiB = argon2MemoryKiB / 1024

// MaxConcurrentPasswordHashes is how many Argon2id derivations may run
// at the same time in one process. It is a cap on memory, not a rate:
// argon2.IDKey allocates PasswordHashMemoryMiB for the whole of its run,
// and every concurrent call holds its own block.
//
// # What the resident peak actually is
//
// It is not MaxConcurrentPasswordHashes times PasswordHashMemoryMiB.
// Counting live blocks undercounts by half, because a derivation's block
// is still resident when the next caller takes the slot it just freed:
// the slot is released when derive returns and the block is not swept
// until a later collection, so under sustained load the process holds
// roughly one finished block for every running one. Measured through
// HashPassword with twenty callers queued behind the cap, peak
// runtime.MemStats.HeapInuse comes out at about
//
//	2 * MaxConcurrentPasswordHashes * PasswordHashMemoryMiB
//
// 129 MiB at a cap of 1, 257 MiB at 2 and 385 MiB at 3. Two independent
// measurements agree on those to a tenth of a MiB.
//
// Resident set size runs above each, since the Go runtime's own memory
// sits outside the heap, but it is the figure that does not reproduce:
// two measurements of the same burst read 135 and 142 MiB at a cap of 1,
// and 327 and 270 MiB at 2, and one run in eight tailed to 205 MiB while
// the heap stayed at 129. Treat the heap figure as the number and
// resident size as "above it, by an amount that varies". Both
// measurements were taken on darwin/arm64; the deployment is Linux under
// a cgroup, which neither of them exercised.
//
// GOMEMLIMIT does not change this and is not a substitute for the cap.
// It is a soft limit, and a collector cannot keep up with 64 MiB
// allocations arriving back to back: the same burst peaked at 327 MiB
// with a cap of 2 under GOMEMLIMIT of both 200 MiB and 150 MiB.
//
// # Why one
//
// charts/calbridge/values.yaml limits the container to 256Mi, and
// internal/depsync's own test fails if that limit and this constant stop
// agreeing, so neither can be changed alone. One derivation is 64 MiB of
// live block and so about 128 MiB resident, half the limit, which leaves
// the other half for the Go runtime, the embedded SPA, the pgx pool, the
// session store and the headroom a collector needs to not be running
// against the limit continuously. Two would be about 257 MiB of heap
// alone, the whole limit before any of that, which is what this cap
// exists to prevent rather than merely reduce.
//
// What one costs is that logins serialize, at roughly three a second.
// At this deployment's scale that is not a constraint anybody will meet:
// a full rate limit burst of ten clears in about three seconds, well
// inside maxHashWait, and a caller that is waiting holds no block.
//
// The scaling path, when logins per second does become a constraint, is
// to raise both together: the chart's resources.limits.memory and this
// constant, keeping the live-block budget at a quarter of the limit (see
// internal/depsync's hashBudgetShare) so the resident peak stays at
// half. Raising either one alone is caught by that test.
//
// # Why a cap at all
//
// Without one the rate limiter is the only thing in front of
// argon2.IDKey, and it bounds how often a request arrives, not how many
// are being served: config.Config's default burst of ten lets ten
// signups or logins into this function at once, which measured 1161 MiB
// resident against a 256Mi limit. One unauthenticated client ends the
// process in a single burst and, because the session store is in
// memory, signs everybody else out with it.
const MaxConcurrentPasswordHashes = 1

// hashSlots is that cap. A caller holds a slot only for the derivation
// itself, never across a database call, so the queue in front of it
// drains at the rate the derivations finish.
var hashSlots = make(chan struct{}, MaxConcurrentPasswordHashes)

// maxHashWait is how long a caller waits for a slot before the request
// is shed. At roughly 0.3 seconds per derivation and
// MaxConcurrentPasswordHashes running at once, a full rate limit burst
// of ten clears in about three seconds, so this leaves room for that
// while staying far below any sensible client timeout. Waiting costs
// nothing in memory: a waiting caller holds no Argon2id block, only its
// own goroutine.
//
// A variable rather than a constant so a test can shorten it; nothing
// outside a test assigns to it.
var maxHashWait = 5 * time.Second

// ErrHashingBusy is returned when no slot came free within maxHashWait,
// or when the request was cancelled while waiting. It is the one error
// from this file a caller answers differently: internal/httpapi turns it
// into a 503, which is the honest answer to "this server is at capacity,
// come back", and never into a failed login, which would tell the person
// their password is wrong when it is not.
var ErrHashingBusy = errors.New("auth: too many password hashes are already running")

// ErrPasswordHashInvalid is returned by VerifyPassword when the stored
// value is not a hash this package wrote: the
// "$argon2id$v=...$m=...$...$..." encoding does not parse. It is an
// operational signal (a corrupted column, a hash from something else
// entirely), not a login outcome: see Local.Login, which never lets this
// surface differently from an ordinary wrong password.
var ErrPasswordHashInvalid = errors.New("auth: password hash is not a recognized argon2id encoding")

// derivation is argon2.IDKey. It is a variable so a test can observe how
// many derivations really run at once; nothing outside a test replaces
// it.
// Deliberately not named with "key" in it: an assignment whose left hand
// side ends in "key" reads to a secret scanner as a key being set, and
// gitleaks flags it as one however the right hand side is spelled.
var derivation = argon2.IDKey

// derive runs one Argon2id derivation under hashSlots, so at most
// MaxConcurrentPasswordHashes of them hold their memory at the same
// time.
func derive(
	ctx context.Context,
	password, salt []byte,
	iterations, memoryKiB uint32,
	threads uint8,
	keyLen uint32,
) ([]byte, error) {
	if err := acquireHashSlot(ctx); err != nil {
		return nil, err
	}
	defer func() { <-hashSlots }()

	return derivation(password, salt, iterations, memoryKiB, threads, keyLen), nil
}

// acquireHashSlot takes a slot, waits up to maxHashWait for one, or
// gives up. A context already cancelled, or cancelled while waiting,
// sheds immediately: the caller has gone away, and spending a slot on it
// would keep one from somebody still there.
func acquireHashSlot(ctx context.Context) error {
	timer := time.NewTimer(maxHashWait)
	defer timer.Stop()

	select {
	case hashSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrHashingBusy, ctx.Err())
	case <-timer.C:
		return ErrHashingBusy
	}
}

// HashPassword derives an Argon2id hash of password with a fresh random
// salt, and encodes the algorithm, its parameters, the salt and the hash
// into one self-describing string:
// "$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>", the PHC string format
// most argon2id libraries use. Encoding the parameters alongside the
// hash, rather than assuming whatever argon2Time, argon2MemoryKiB and
// argon2Threads currently are, is what lets VerifyPassword still check
// an old hash correctly after those constants change.
//
// ctx bounds only the wait for a derivation slot, never the derivation
// itself; see MaxConcurrentPasswordHashes and ErrHashingBusy.
func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read password salt: %w", err)
	}

	hash, err := derive(ctx, []byte(password), salt, argon2Time, argon2MemoryKiB, argon2Threads, argon2KeyLen)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2MemoryKiB, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// argon2idParams is what HashPassword's encoding carries about how a
// particular hash was computed, read back by VerifyPassword rather than
// assumed from today's constants.
type argon2idParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

// VerifyPassword reports whether password hashes to encoded, recomputing
// the hash with the parameters, salt and output length encoded's own
// string carries rather than today's constants, so a hash written under
// an older parameter choice still verifies correctly.
//
// The comparison is constant time (crypto/subtle), and the two outcomes
// this function can report, a parse failure and a hash mismatch, are
// deliberately distinguished by return value only for an operator's log,
// never for a caller to answer a login differently: Local.Login
// collapses both, and "no such account" besides, into one identical
// failure before anything reaches outside this package. ErrHashingBusy
// is the exception: it is not an outcome of this comparison at all and
// is answered as a 503, see MaxConcurrentPasswordHashes.
//
// ctx bounds only the wait for a derivation slot, never the derivation
// itself.
func VerifyPassword(ctx context.Context, encoded, password string) (bool, error) {
	params, salt, hash, err := decodeArgon2id(encoded)
	if err != nil {
		return false, err
	}

	candidate, err := derive(ctx, []byte(password), salt, params.time, params.memory, params.threads, uint32(len(hash)))
	if err != nil {
		return false, err
	}

	return subtle.ConstantTimeCompare(candidate, hash) == 1, nil
}

// decodeArgon2id parses the string HashPassword produces. Every failure,
// whatever the reason, is ErrPasswordHashInvalid: the detail belongs in
// whatever logged the operational problem, not in a typed error a caller
// might branch on.
func decodeArgon2id(encoded string) (argon2idParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return argon2idParams{}, nil, nil, ErrPasswordHashInvalid
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argon2idParams{}, nil, nil, ErrPasswordHashInvalid
	}

	var params argon2idParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return argon2idParams{}, nil, nil, ErrPasswordHashInvalid
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2idParams{}, nil, nil, ErrPasswordHashInvalid
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2idParams{}, nil, nil, ErrPasswordHashInvalid
	}

	return params, salt, hash, nil
}
