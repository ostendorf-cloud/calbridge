// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package storage

import "errors"

var (
	// ErrNotFound is returned when a repository lookup matches no row.
	ErrNotFound = errors.New("not found")

	// ErrConflict is returned when a write would violate a uniqueness
	// constraint: a user's email, an identity's (provider, subject) pair,
	// a calendar already tracked for a connection, or a grant already
	// recorded for the same owner, grantee and calendar.
	ErrConflict = errors.New("conflict")

	// ErrNoUserScope is returned by a user-scoped repository method
	// called on a context that UserScope.WithUser did not produce. There
	// is deliberately no fallback to an unscoped query; see UserScope.
	ErrNoUserScope = errors.New("no user scope set on context")
)
