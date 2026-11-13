// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
)

// concurrencyProbe stands in for the Argon2id derivation and records the
// most derivations that were ever running at the same time.
//
// Each call holds its slot until release is closed, so every caller that
// got through piles up and the peak is the number the limit actually
// allows, not whatever the scheduler happened to interleave.
type concurrencyProbe struct {
	mu      sync.Mutex
	running int
	peak    int

	// entered is signalled once per call, so a test can wait until the
	// derivations it expects to start really have.
	entered chan struct{}
	// release holds every call inside the derivation until closed.
	release chan struct{}
}

func newConcurrencyProbe(expected int) *concurrencyProbe {
	return &concurrencyProbe{
		entered: make(chan struct{}, expected),
		release: make(chan struct{}),
	}
}

// derive is what auth.SetDeriveKey installs in place of argon2.IDKey.
func (p *concurrencyProbe) derive(_, _ []byte, _, _ uint32, _ uint8, keyLen uint32) []byte {
	p.mu.Lock()
	p.running++

	if p.running > p.peak {
		p.peak = p.running
	}
	p.mu.Unlock()

	p.entered <- struct{}{}
	<-p.release

	p.mu.Lock()
	p.running--
	p.mu.Unlock()

	return make([]byte, keyLen)
}

func (p *concurrencyProbe) peakConcurrency() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.peak
}

// TestPasswordHashingRunsNoMoreThanItsMemoryBudgetAllows is what stands
// between one unauthenticated client and an OOM-killed pod.
//
// Argon2id holds auth.PasswordHashMemoryMiB for the whole of each
// derivation, so the number running at once is the number of blocks
// live at once, and no collector can reduce it. The rate limiter in
// front of these routes bounds how often a request arrives, not how many
// are being served, so without the cap under test a full burst enters
// this function together.
//
// Removing the cap makes this fail: every caller below reaches the
// derivation, and the peak becomes the number of callers.
//
// Not parallel on purpose: it replaces package state. See
// auth.SetDeriveKey.
func TestPasswordHashingRunsNoMoreThanItsMemoryBudgetAllows(t *testing.T) {
	const callers = 10

	probe := newConcurrencyProbe(callers)
	auth.SetDeriveKey(t, probe.derive)

	var wg sync.WaitGroup

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = auth.HashPassword(context.Background(), "a-long-enough-test-passphrase")
		}()
	}

	// Wait for as many derivations as the cap allows to really be
	// inside the probe, so the peak below is measured against work that
	// started rather than against a race the test won.
	for range auth.MaxConcurrentPasswordHashes {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("not even the allowed number of derivations started")
		}
	}

	// Nothing more may enter while those are still holding their slots.
	// A short wait is the whole assertion: an eleventh derivation would
	// have to start in this window for the cap to be missing, and with
	// ten callers already blocked on it, it would.
	select {
	case <-probe.entered:
		close(probe.release)
		wg.Wait()
		t.Fatalf("more than %d derivations ran at once (peak %d): every one of them holds %d MiB, "+
			"so the cap is the only thing keeping a burst inside the pod's memory limit",
			auth.MaxConcurrentPasswordHashes, probe.peakConcurrency(), auth.PasswordHashMemoryMiB)
	case <-time.After(250 * time.Millisecond):
	}

	if got := probe.peakConcurrency(); got > auth.MaxConcurrentPasswordHashes {
		t.Errorf("peak concurrent derivations = %d, want at most %d", got, auth.MaxConcurrentPasswordHashes)
	}

	close(probe.release)
	wg.Wait()

	if got := probe.peakConcurrency(); got > auth.MaxConcurrentPasswordHashes {
		t.Errorf("peak concurrent derivations = %d over the whole run, want at most %d",
			got, auth.MaxConcurrentPasswordHashes)
	}
}

// TestPasswordHashingShedsRatherThanQueueingForever: a caller whose
// request is already gone does not wait for a slot, and does not get one
// at the expense of somebody still there. The answer is ErrHashingBusy,
// which internal/httpapi turns into a 503, never a failed login.
//
// Not parallel on purpose: it replaces package state.
func TestPasswordHashingShedsRatherThanQueueingForever(t *testing.T) {
	probe := newConcurrencyProbe(auth.MaxConcurrentPasswordHashes)
	auth.SetDeriveKey(t, probe.derive)

	var wg sync.WaitGroup

	// Fill every slot and hold them.
	for range auth.MaxConcurrentPasswordHashes {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = auth.HashPassword(context.Background(), "a-long-enough-test-passphrase")
		}()
	}

	for range auth.MaxConcurrentPasswordHashes {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the derivations that fill the slots never started")
		}
	}

	err := shedOrTimeOut(t, func(ctx context.Context) error {
		_, err := auth.HashPassword(ctx, "a-long-enough-test-passphrase")

		return err
	})

	if !errors.Is(err, auth.ErrHashingBusy) {
		t.Fatalf("HashPassword() error = %v, want ErrHashingBusy for a cancelled request with no slot free", err)
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("HashPassword() error = %v, want it to still name the cancellation for a log line", err)
	}

	close(probe.release)
	wg.Wait()

	// With the slots free again, the next caller is served rather than
	// shed: the cap is a bound on concurrency, not a circuit that stays
	// open.
	if _, err := auth.HashPassword(context.Background(), "a-long-enough-test-passphrase"); err != nil {
		t.Fatalf("HashPassword() after the slots freed error = %v, want it to succeed", err)
	}
}

// TestVerifyPasswordIsUnderTheSameCap: the login path derives too, and a
// login is the easier of the two routes to flood, since it needs no
// unused address.
//
// Not parallel on purpose: it replaces package state.
func TestVerifyPasswordIsUnderTheSameCap(t *testing.T) {
	encoded, err := auth.HashPassword(context.Background(), "a-long-enough-test-passphrase")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	probe := newConcurrencyProbe(auth.MaxConcurrentPasswordHashes)
	auth.SetDeriveKey(t, probe.derive)

	var wg sync.WaitGroup

	for range auth.MaxConcurrentPasswordHashes {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = auth.VerifyPassword(context.Background(), encoded, "a-long-enough-test-passphrase")
		}()
	}

	for range auth.MaxConcurrentPasswordHashes {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the verifications never started")
		}
	}

	err = shedOrTimeOut(t, func(ctx context.Context) error {
		_, err := auth.VerifyPassword(ctx, encoded, "a-long-enough-test-passphrase")

		return err
	})
	if !errors.Is(err, auth.ErrHashingBusy) {
		t.Fatalf("VerifyPassword() error = %v, want ErrHashingBusy with every slot held", err)
	}

	close(probe.release)
	wg.Wait()
}

// shedOrTimeOut runs call with an already cancelled context and returns
// what it answered, or fails the test if it did not answer at all.
//
// The call is made from a goroutine on purpose: with the cap removed,
// nothing stops this derivation from starting, and it would then block
// inside the probe until the test that is waiting for it releases it,
// which it cannot. A test that hangs says far less than one that fails,
// so the wait is bounded here.
func shedOrTimeOut(t *testing.T, call func(context.Context) error) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	answered := make(chan error, 1)

	go func() { answered <- call(ctx) }()

	select {
	case err := <-answered:
		return err
	case <-time.After(2 * auth.MaxHashWait):
		t.Fatal("the call never answered: with every derivation slot held and the request already " +
			"cancelled, it must be shed rather than start a derivation of its own")

		return nil
	}
}
