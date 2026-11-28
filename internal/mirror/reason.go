// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package mirror

import (
	"errors"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// reason classifies the error of a pass as one fixed word for a log line.
//
// A pass error wraps whatever a provider and a server wrote, and a log line
// must never carry that text: nothing here proves a server did not echo the
// credential it was sent. The edge, grant and user ids the line also
// carries say where to look, and the word says what kind of failure it was.
func reason(err error) string {
	switch {
	case errors.Is(err, sync.ErrGrantMissing):
		return "grant_missing"
	case errors.Is(err, sync.ErrGrantExceeded):
		return "grant_exceeded"
	case errors.Is(err, sync.ErrLoop):
		return "loop"
	case errors.Is(err, sync.ErrInvalidBinding):
		return "invalid_binding"
	case errors.Is(err, sync.ErrTooManyPages):
		return "too_many_pages"
	case errors.Is(err, storage.ErrNotFound):
		return "not_found"
	case errors.Is(err, storage.ErrConflict):
		return "conflict"
	}

	return provider.Reason(err)
}
