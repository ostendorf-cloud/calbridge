// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package storagetest

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// sharing satisfies storage.SharingRepository. It imitates the same
// three bounds the real one gets from the schema: a shared calendar is
// reachable only through a grant naming this user as grantee, a
// counterparty is only somebody on the other side of such a grant, and
// the address lookup is not scoped at all, matching the SECURITY
// DEFINER function the real one goes through.
type sharing struct{ store *Store }

// ListCalendarsSharedWithMe implements storage.SharingRepository.
func (r *sharing) ListCalendarsSharedWithMe(_ context.Context) ([]storage.SharedCalendar, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	var found []storage.SharedCalendar

	for _, grantID := range r.store.grantOrder {
		grant, ok := r.store.grants[grantID]
		if !ok || grant.GranteeUserID != scope {
			continue
		}

		calendar, ok := r.store.calendars[grant.CalendarID]
		if !ok {
			continue
		}

		found = append(found, storage.SharedCalendar{
			Calendar:       calendar,
			GrantID:        grant.ID,
			MaxDetailLevel: grant.MaxDetailLevel,
		})
	}

	return found, nil
}

// ListGrantCounterparties implements storage.SharingRepository.
func (r *sharing) ListGrantCounterparties(_ context.Context) ([]storage.GrantCounterparty, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	scope, err := r.store.scoped()
	if err != nil {
		return nil, err
	}

	seen := map[uuid.UUID]bool{}

	var found []storage.GrantCounterparty

	for _, grantID := range r.store.grantOrder {
		grant, ok := r.store.grants[grantID]
		if !ok {
			continue
		}

		var other uuid.UUID

		switch scope {
		case grant.OwnerUserID:
			other = grant.GranteeUserID
		case grant.GranteeUserID:
			other = grant.OwnerUserID
		default:
			continue
		}

		user, ok := r.store.users[other]
		if !ok || seen[other] {
			continue
		}

		seen[other] = true

		found = append(found, storage.GrantCounterparty{UserID: user.ID, Email: user.Email})
	}

	return found, nil
}

// FindUserIDByEmail implements storage.SharingRepository. Not scoped,
// matching the real repository.
func (r *sharing) FindUserIDByEmail(_ context.Context, email string) (uuid.UUID, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for _, user := range r.store.users {
		if strings.EqualFold(user.Email, email) {
			return user.ID, nil
		}
	}

	return uuid.Nil, storage.ErrNotFound
}
