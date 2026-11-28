// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

func TestReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error", err: nil, want: provider.ReasonUnclassified},
		{name: "refused credential", err: provider.ErrCredentialRejected, want: provider.ReasonCredentialRefused},
		{name: "missing credential", err: provider.ErrCredentialMissing, want: provider.ReasonCredentialMissing},
		{name: "credential of another provider", err: provider.ErrCredentialMismatch, want: provider.ReasonCredentialOther},
		{name: "unsupported operation", err: provider.ErrUnsupported, want: provider.ReasonUnsupported},
		{name: "unknown provider", err: provider.ErrUnknownProvider, want: provider.ReasonUnknownProvider},
		{name: "unknown calendar", err: provider.ErrUnknownCalendar, want: provider.ReasonUnknownCalendar},
		{name: "unknown event", err: provider.ErrUnknownEvent, want: provider.ReasonUnknownEvent},
		{name: "stale sync token", err: provider.ErrSyncTokenInvalid, want: provider.ReasonSyncTokenInvalid},
		{name: "invalid event", err: provider.ErrInvalidEvent, want: provider.ReasonInvalidEvent},
		{name: "invalid change", err: provider.ErrInvalidChange, want: provider.ReasonInvalidChange},
		{name: "deadline", err: context.DeadlineExceeded, want: provider.ReasonTimeout},
		{name: "cancellation", err: context.Canceled, want: provider.ReasonCanceled},
		{
			name: "transport failure",
			err:  &url.Error{Op: "Get", URL: "https://caldav.example.com/", Err: errors.New("connection refused")},
			want: provider.ReasonNetwork,
		},
		{
			name: "wrapped sentinel",
			err:  fmt.Errorf("list calendars: %w", fmt.Errorf("caldav: %w", provider.ErrCredentialRejected)),
			want: provider.ReasonCredentialRefused,
		},
		{name: "joined errors", err: errors.Join(errors.New("first"), provider.ErrUnknownEvent), want: provider.ReasonUnknownEvent},
		{name: "text no sentinel names", err: errors.New("the credential was refused"), want: provider.ReasonUnclassified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := provider.Reason(tt.err); got != tt.want {
				t.Errorf("Reason() = %q, want %q", got, tt.want)
			}
		})
	}
}
