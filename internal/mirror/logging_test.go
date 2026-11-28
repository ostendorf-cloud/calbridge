// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package mirror_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/mirror"
	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// logBuffer is a bytes.Buffer a logger may write to while a test reads it.
type logBuffer struct {
	mu  stdsync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

const leakedText = "wpgk-hunter2-fixture"

// leakyError is what a provider error would look like if a server echoed
// the request it was sent into its answer.
func leakyError(cause error) error {
	basic := base64.StdEncoding.EncodeToString([]byte("user@example.com:" + leakedText))

	return fmt.Errorf("caldav MKCALENDAR: server said Authorization: Basic %s for password %s: %w",
		basic, leakedText, cause)
}

func TestService_LogsNoProviderText(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name       string
		cause      error
		wantReason string
	}{
		{name: "credential refused", cause: provider.ErrCredentialRejected, wantReason: "reason=credential_refused"},
		{name: "unclassified failure", cause: errors.New("connection reset"), wantReason: "reason=unclassified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each trigger below ends in one of the three log calls that
			// carry a pass error, and every one has to stay clean.
			triggers := []struct {
				name string
				// scheduled keeps the edge owners visible, so Pass finds
				// them. The queued triggers hide them, to be told apart
				// from the pass Run makes at start.
				scheduled bool
				run       func(t *testing.T, w *world, engine *fakeEngine, service *mirror.Service, edge storage.Edge)
				ids       func(w *world, edge storage.Edge) []string
			}{
				{
					name:      "scheduled pass over an edge",
					scheduled: true,
					run: func(_ *testing.T, _ *world, engine *fakeEngine, service *mirror.Service, _ storage.Edge) {
						engine.setReconcile(leakyError(tt.cause))
						service.Pass(ctx)
					},
					ids: func(w *world, edge storage.Edge) []string {
						return []string{edge.ID.String(), w.grantee.String()}
					},
				},
				{
					name: "queued pass over a new edge",
					run: func(t *testing.T, w *world, engine *fakeEngine, service *mirror.Service, edge storage.Edge) {
						t.Helper()

						engine.setReconcile(leakyError(tt.cause))

						stop := runUntil(t, service)
						service.EdgeCreated(w.grantee, edge.ID)
						waitFor(t, engine.reconcileCh)
						stop()
					},
					ids: func(_ *world, edge storage.Edge) []string { return []string{edge.ID.String()} },
				},
				{
					name: "teardown of a revoked grant",
					run: func(t *testing.T, w *world, engine *fakeEngine, service *mirror.Service, _ storage.Edge) {
						t.Helper()

						engine.enforceErr = leakyError(tt.cause)

						stop := runUntil(t, service)
						service.GrantRevoked(w.grant)
						waitFor(t, engine.enforcedCh)
						stop()
					},
					ids: func(w *world, _ storage.Edge) []string { return []string{w.grant.ID.String()} },
				},
			}

			for _, trigger := range triggers {
				t.Run(trigger.name, func(t *testing.T) {
					t.Parallel()

					w := newWorld(t)
					edge := w.edge(ctx, t, w.own.ID, storage.DetailLevelFull)
					engine := newFakeEngine(w.store)

					var (
						logs  logBuffer
						store mirror.Store = noOwners{w.store}
					)

					if trigger.scheduled {
						store = w.store
					}

					service, err := mirror.New(mirror.Options{
						Store:    store,
						Engine:   engine,
						Binder:   newBinder(t, w),
						Interval: time.Hour,
						Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
					})
					if err != nil {
						t.Fatalf("mirror.New() error = %v", err)
					}

					trigger.run(t, w, engine, service, edge)

					got := logs.String()
					if got == "" {
						t.Fatal("the service logged nothing for a failed pass")
					}

					for _, secret := range []string{
						leakedText,
						base64.StdEncoding.EncodeToString([]byte("user@example.com:" + leakedText)),
						"Authorization",
						"MKCALENDAR",
					} {
						if strings.Contains(got, secret) {
							t.Errorf("the log carries %q:\n%s", secret, got)
						}
					}

					if !strings.Contains(got, tt.wantReason) {
						t.Errorf("the log lacks %q:\n%s", tt.wantReason, got)
					}

					for _, id := range trigger.ids(w, edge) {
						if !strings.Contains(got, id) {
							t.Errorf("the log lacks the id %s:\n%s", id, got)
						}
					}
				})
			}
		})
	}
}
