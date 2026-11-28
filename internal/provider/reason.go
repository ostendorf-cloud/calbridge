// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider

import (
	"context"
	"errors"
	"net"
)

// Reasons Reason answers with. A log line carries one of these instead of
// the error itself.
const (
	ReasonUnclassified      = "unclassified"
	ReasonCredentialRefused = "credential_refused"
	ReasonCredentialMissing = "credential_missing"
	ReasonCredentialOther   = "credential_for_another_provider"
	ReasonUnsupported       = "unsupported"
	ReasonUnknownProvider   = "unknown_provider"
	ReasonUnknownCalendar   = "unknown_calendar"
	ReasonUnknownEvent      = "unknown_event"
	ReasonSyncTokenInvalid  = "sync_token_invalid"
	ReasonInvalidEvent      = "invalid_event"
	ReasonInvalidChange     = "invalid_change"
	ReasonTimeout           = "timeout"
	ReasonCanceled          = "canceled"
	ReasonNetwork           = "network"
)

// Reason classifies an error a connector or the code around one returned,
// as one fixed word, so a log line can say what went wrong without carrying
// the error.
//
// An error from a provider is text a connector wrote around text a server
// wrote. Neither is under this repository's control once a credential is
// involved: a server can echo a request header into a status line, and a
// transport can quote what it was handed. The word returned here is picked
// by comparing the error against the sentinels this package defines, so no
// byte of the error itself can reach the caller. An error matching no
// sentinel is ReasonUnclassified.
func Reason(err error) string {
	switch {
	case errors.Is(err, ErrCredentialRejected):
		return ReasonCredentialRefused
	case errors.Is(err, ErrCredentialMissing):
		return ReasonCredentialMissing
	case errors.Is(err, ErrCredentialMismatch):
		return ReasonCredentialOther
	case errors.Is(err, ErrUnsupported):
		return ReasonUnsupported
	case errors.Is(err, ErrUnknownProvider):
		return ReasonUnknownProvider
	case errors.Is(err, ErrUnknownCalendar):
		return ReasonUnknownCalendar
	case errors.Is(err, ErrUnknownEvent):
		return ReasonUnknownEvent
	case errors.Is(err, ErrSyncTokenInvalid):
		return ReasonSyncTokenInvalid
	case errors.Is(err, ErrInvalidEvent):
		return ReasonInvalidEvent
	case errors.Is(err, ErrInvalidChange):
		return ReasonInvalidChange
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout
	case errors.Is(err, context.Canceled):
		return ReasonCanceled
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return ReasonNetwork
	}

	return ReasonUnclassified
}
