// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage/storagetest"
)

// logBuffer is a bytes.Buffer a logger may write to from a handler
// goroutine while the test reads it.
type logBuffer struct {
	mu  sync.Mutex
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

// leakyProviderError is what a provider error would look like if a server
// echoed the request it was sent into its answer.
func leakyProviderError(cause error) error {
	basic := base64.StdEncoding.EncodeToString([]byte("user@example.com:" + leakedText))

	return fmt.Errorf("caldav MKCALENDAR: server said Authorization: Basic %s for password %s: %w",
		basic, leakedText, cause)
}

// graphHandlersLogging is graphHandlersWithMirror with the handlers' logger
// writing into the returned buffer.
func graphHandlersLogging(t *testing.T) (*storagetest.Store, string, *fakeMirror, *logBuffer) {
	t.Helper()

	var logs logBuffer

	deps := testDeps(t)
	deps.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	store := testStore(t, deps)

	mirror, ok := deps.Mirror.(*fakeMirror)
	if !ok {
		t.Fatalf("deps.Mirror is a %T, want the fake mirror", deps.Mirror)
	}

	mirror.store = store

	_, baseURL := testClient(t, build(t, deps))

	return store, baseURL, mirror, &logs
}

func requireNoProviderText(t *testing.T, logs *logBuffer) {
	t.Helper()

	got := logs.String()

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
}

func TestAFailedDiscardLogsNoProviderText(t *testing.T) {
	serial(t)

	store, baseURL, mirror, logs := graphHandlersLogging(t)

	owner := signIn(t, store, baseURL, testEmail)
	account, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

	// The source goes between the handler's two scopes, so the edge is
	// refused and the calendar the provider made is discarded, and the
	// discard fails with an error carrying the fixture password.
	mirror.afterCreate = func() {
		if err := store.WithUser(t.Context(), owner.userID, func(ctx context.Context) error {
			return store.Calendars().Delete(ctx, source.ID)
		}); err != nil {
			t.Errorf("delete the source: %v", err)
		}
	}
	mirror.discardErr = leakyProviderError(provider.ErrCredentialRejected)

	refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges",
		createTargetBody(source.ID, account.ID), owner.csrf))
	if refused.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", refused.status, http.StatusNotFound)
	}

	requireNoProviderText(t, logs)

	got := logs.String()
	for _, want := range []string{"reason=credential_refused", account.ID.String()} {
		if !strings.Contains(got, want) {
			t.Errorf("the log lacks %q:\n%s", want, got)
		}
	}

	// A CalDAV calendar's id is a path that names the account.
	for _, unwanted := range []string{"external_id", "provider-calendar-1"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the log carries %q:\n%s", unwanted, got)
		}
	}
}

func TestAnUnanswerableProviderFailureLogsNoProviderText(t *testing.T) {
	serial(t)

	t.Run("creating the target calendar", func(t *testing.T) {
		serial(t)

		store, baseURL, mirror, logs := graphHandlersLogging(t)
		mirror.createErr = leakyProviderError(errors.New("connection reset"))

		owner := signIn(t, store, baseURL, testEmail)
		account, source := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")

		refused := readRefusal(t, do(t, owner.client, http.MethodPost, baseURL+"/api/v1/edges",
			createTargetBody(source.ID, account.ID), owner.csrf))
		if refused.status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", refused.status, http.StatusInternalServerError)
		}

		requireNoProviderText(t, logs)

		if got := logs.String(); !strings.Contains(got, "create target calendar: unclassified") {
			t.Errorf("the log names no step and reason:\n%s", got)
		}
	})

	t.Run("deleting an edge", func(t *testing.T) {
		serial(t)

		store, baseURL, mirror, logs := graphHandlersLogging(t)

		owner := signIn(t, store, baseURL, testEmail)
		_, first := seedAccountCalendar(t, store, owner.userID, "Work Google", "Work")
		_, second := seedAccountCalendar(t, store, owner.userID, "Private Google", "Private")

		edge := createEdgeFor(t, owner, baseURL, first.ID, second.ID)
		mirror.deleteErr = leakyProviderError(provider.ErrUnsupported)

		refused := readRefusal(t, do(t, owner.client, http.MethodDelete, baseURL+"/api/v1/edges/"+edge, "", owner.csrf))
		if refused.status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", refused.status, http.StatusInternalServerError)
		}

		requireNoProviderText(t, logs)

		if got := logs.String(); !strings.Contains(got, "delete edge: unsupported") {
			t.Errorf("the log names no step and reason:\n%s", got)
		}
	})
}
