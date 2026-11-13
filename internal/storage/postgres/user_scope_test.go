// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
)

// TestRepositoriesRequireUserScope proves the fail-loud contract: a
// user-scoped repository method called on a context WithUser did not
// produce fails immediately with storage.ErrNoUserScope, before it ever
// touches a connection. None of these need a live database, by
// construction: txFromContext rejects the context before any query
// would run.
func TestRepositoriesRequireUserScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := uuid.New()

	// A nil encryptor is safe here: every case below fails in
	// txFromContext, before connectionRepository or
	// eventMappingRepository would ever reach it.
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "IdentityRepository.Create",
			call: func() error {
				_, err := (&identityRepository{}).Create(ctx, storage.Identity{})
				return err
			},
		},
		{
			name: "IdentityRepository.FindByID",
			call: func() error {
				_, err := (&identityRepository{}).FindByID(ctx, id)
				return err
			},
		},
		{
			name: "IdentityRepository.List",
			call: func() error {
				_, err := (&identityRepository{}).List(ctx)
				return err
			},
		},
		{
			name: "IdentityRepository.Delete",
			call: func() error {
				return (&identityRepository{}).Delete(ctx, id)
			},
		},
		{
			name: "ConnectionRepository.Create",
			call: func() error {
				_, err := (&connectionRepository{}).Create(ctx, storage.Connection{})
				return err
			},
		},
		{
			name: "ConnectionRepository.FindByID",
			call: func() error {
				_, err := (&connectionRepository{}).FindByID(ctx, id)
				return err
			},
		},
		{
			name: "ConnectionRepository.List",
			call: func() error {
				_, err := (&connectionRepository{}).List(ctx)
				return err
			},
		},
		{
			name: "ConnectionRepository.UpdateCredentials",
			call: func() error {
				_, err := (&connectionRepository{}).UpdateCredentials(ctx, id, nil)
				return err
			},
		},
		{
			name: "ConnectionRepository.Delete",
			call: func() error {
				return (&connectionRepository{}).Delete(ctx, id)
			},
		},
		{
			name: "CalendarRepository.Create",
			call: func() error {
				_, err := (&calendarRepository{}).Create(ctx, storage.Calendar{})
				return err
			},
		},
		{
			name: "CalendarRepository.FindByID",
			call: func() error {
				_, err := (&calendarRepository{}).FindByID(ctx, id)
				return err
			},
		},
		{
			name: "CalendarRepository.List",
			call: func() error {
				_, err := (&calendarRepository{}).List(ctx)
				return err
			},
		},
		{
			name: "CalendarRepository.Delete",
			call: func() error {
				return (&calendarRepository{}).Delete(ctx, id)
			},
		},
		{
			name: "EdgeRepository.Create",
			call: func() error {
				_, err := (&edgeRepository{}).Create(ctx, storage.Edge{})
				return err
			},
		},
		{
			name: "EdgeRepository.FindByID",
			call: func() error {
				_, err := (&edgeRepository{}).FindByID(ctx, id)
				return err
			},
		},
		{
			name: "EdgeRepository.List",
			call: func() error {
				_, err := (&edgeRepository{}).List(ctx)
				return err
			},
		},
		{
			name: "EdgeRepository.UpdateDetailLevel",
			call: func() error {
				_, err := (&edgeRepository{}).UpdateDetailLevel(ctx, id, storage.DetailLevelFull)
				return err
			},
		},
		{
			name: "EdgeRepository.Delete",
			call: func() error {
				return (&edgeRepository{}).Delete(ctx, id)
			},
		},
		{
			name: "CalendarGrantRepository.Create",
			call: func() error {
				_, err := (&calendarGrantRepository{}).Create(ctx, storage.CalendarGrant{})
				return err
			},
		},
		{
			name: "CalendarGrantRepository.FindByID",
			call: func() error {
				_, err := (&calendarGrantRepository{}).FindByID(ctx, id)
				return err
			},
		},
		{
			name: "CalendarGrantRepository.List",
			call: func() error {
				_, err := (&calendarGrantRepository{}).List(ctx)
				return err
			},
		},
		{
			name: "CalendarGrantRepository.Delete",
			call: func() error {
				return (&calendarGrantRepository{}).Delete(ctx, id)
			},
		},
		{
			name: "EventMappingRepository.Create",
			call: func() error {
				_, err := (&eventMappingRepository{}).Create(ctx, storage.EventMapping{})
				return err
			},
		},
		{
			name: "EventMappingRepository.FindByID",
			call: func() error {
				_, err := (&eventMappingRepository{}).FindByID(ctx, id)
				return err
			},
		},
		{
			name: "EventMappingRepository.FindByEdgeAndSourceEventID",
			call: func() error {
				_, err := (&eventMappingRepository{}).FindByEdgeAndSourceEventID(ctx, id, "source-event")
				return err
			},
		},
		{
			name: "EventMappingRepository.Update",
			call: func() error {
				_, err := (&eventMappingRepository{}).Update(ctx, storage.EventMapping{})
				return err
			},
		},
		{
			name: "EventMappingRepository.Delete",
			call: func() error {
				return (&eventMappingRepository{}).Delete(ctx, id)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.call()
			if !errors.Is(err, storage.ErrNoUserScope) {
				t.Errorf("error = %v, want errors.Is(err, storage.ErrNoUserScope)", err)
			}
		})
	}
}
