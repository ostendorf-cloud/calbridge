// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/sync"
)

// BinderStore is the part of the storage layer the binder reads. Every
// repository is reached inside a WithUser scope the binder opens.
type BinderStore interface {
	storage.UserScope

	Calendars() storage.CalendarRepository
	Connections() storage.ConnectionRepository
	Sharing() storage.SharingRepository
}

// Binder resolves an edge to the two endpoints it connects. It is the
// sync.Binder the engine is built with.
//
// It is the only place that holds a decrypted credential next to the
// connector that can use it: the storage layer decrypts the stored bytes,
// the registry names the connector by the connection's provider id, and
// the connector's own decoder turns the bytes into its credential type.
type Binder struct {
	store    BinderStore
	registry *provider.Registry
}

// NewBinder builds a binder over a store and a connector registry. A
// binder with either missing could not resolve any edge, so it is refused
// here rather than at the first pass.
func NewBinder(store BinderStore, registry *provider.Registry) (*Binder, error) {
	var missing []string

	if store == nil {
		missing = append(missing, "store")
	}
	if registry == nil {
		missing = append(missing, "registry")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("binder is missing %s: %w", strings.Join(missing, ", "), ErrIncomplete)
	}

	return &Binder{store: store, registry: registry}, nil
}

// Bind implements sync.Binder.
//
// A source the edge's user can no longer reach, which is what a revoked
// share looks like, comes back as the zero Endpoint and no error. The
// engine's teardown needs only the target, and refusing to bind would
// leave an edge nobody can take down. A reconcile over such a binding
// fails the engine's own validation, so the zero endpoint cannot be
// mistaken for a working one.
func (b *Binder) Bind(ctx context.Context, edge storage.Edge) (sync.Binding, error) {
	mask, err := sync.MaskFor(edge.DetailLevel)
	if err != nil {
		return sync.Binding{}, fmt.Errorf("bind edge %s: %w", edge.ID, err)
	}

	target, err := b.owned(ctx, edge.UserID, edge.TargetCalendarID)
	if err != nil {
		return sync.Binding{}, fmt.Errorf("bind edge %s target: %w", edge.ID, err)
	}

	source, err := b.source(ctx, edge)
	if err != nil {
		return sync.Binding{}, fmt.Errorf("bind edge %s source: %w", edge.ID, err)
	}

	return sync.Binding{Edge: edge, Mask: mask, Source: source, Target: target}, nil
}

// Connect resolves one of a user's own connections to its connector and
// the credential it holds.
func (b *Binder) Connect(
	ctx context.Context,
	userID, connectionID uuid.UUID,
) (provider.Connector, provider.Credential, error) {
	var connection storage.Connection

	if err := b.store.WithUser(ctx, userID, func(ctx context.Context) error {
		found, err := b.store.Connections().FindByID(ctx, connectionID)
		if err != nil {
			return err
		}
		connection = found

		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("read connection %s: %w", connectionID, err)
	}

	return b.credentialOf(connection)
}

// owned resolves a calendar the user owns.
func (b *Binder) owned(ctx context.Context, userID, calendarID uuid.UUID) (sync.Endpoint, error) {
	calendar, connection, err := b.read(ctx, userID, calendarID, uuid.Nil)
	if err != nil {
		return sync.Endpoint{}, err
	}

	return b.endpoint(calendar, connection)
}

// source resolves the calendar an edge reads from. It is the user's own
// calendar, or one somebody shared with them.
//
// A shared calendar belongs to its owner, and so does the connection
// behind it: neither is visible in the edge owner's scope, and widening
// that policy is exactly what the schema refuses. The binder reads them in
// the owner's scope instead, and only after the edge owner's own scope has
// confirmed that a grant names the calendar. The grant, not the binder's
// reach, is what admits the read.
func (b *Binder) source(ctx context.Context, edge storage.Edge) (sync.Endpoint, error) {
	var (
		own    bool
		shared storage.SharedCalendar
		found  bool
	)

	err := b.store.WithUser(ctx, edge.UserID, func(ctx context.Context) error {
		_, err := b.store.Calendars().FindByID(ctx, edge.SourceCalendarID)
		if err == nil {
			own = true

			return nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return err
		}

		all, err := b.store.Sharing().ListCalendarsSharedWithMe(ctx)
		if err != nil {
			return err
		}

		for _, calendar := range all {
			if calendar.ID == edge.SourceCalendarID {
				shared, found = calendar, true

				break
			}
		}

		return nil
	})
	if err != nil {
		return sync.Endpoint{}, fmt.Errorf("find calendar %s: %w", edge.SourceCalendarID, err)
	}

	switch {
	case own:
		return b.owned(ctx, edge.UserID, edge.SourceCalendarID)
	case !found:
		return sync.Endpoint{}, nil
	}

	calendar, connection, err := b.read(ctx, shared.UserID, shared.ID, shared.ConnectionID)
	if err != nil {
		return sync.Endpoint{}, err
	}

	return b.endpoint(calendar, connection)
}

// read loads one calendar and its connection in one user's scope. A zero
// connectionID reads the connection the calendar itself names.
func (b *Binder) read(
	ctx context.Context,
	userID, calendarID, connectionID uuid.UUID,
) (storage.Calendar, storage.Connection, error) {
	var (
		calendar   storage.Calendar
		connection storage.Connection
	)

	if err := b.store.WithUser(ctx, userID, func(ctx context.Context) error {
		found, err := b.store.Calendars().FindByID(ctx, calendarID)
		if err != nil {
			return fmt.Errorf("read calendar %s: %w", calendarID, err)
		}
		calendar = found

		if connectionID == uuid.Nil {
			connectionID = found.ConnectionID
		}

		stored, err := b.store.Connections().FindByID(ctx, connectionID)
		if err != nil {
			return fmt.Errorf("read connection %s: %w", connectionID, err)
		}
		connection = stored

		return nil
	}); err != nil {
		return storage.Calendar{}, storage.Connection{}, err
	}

	return calendar, connection, nil
}

// endpoint turns a stored calendar and its connection into what the
// engine reaches a provider with.
//
// TimeZone stays empty: nothing stores a calendar's zone, and the engine
// reads it only to anchor a floating time for a connector that holds none.
// The CalDAV connector does, so the gap shows only once a connector
// without floating times is registered.
func (b *Binder) endpoint(calendar storage.Calendar, connection storage.Connection) (sync.Endpoint, error) {
	connector, credential, err := b.credentialOf(connection)
	if err != nil {
		return sync.Endpoint{}, err
	}

	return sync.Endpoint{
		Connector:  connector,
		Credential: credential,
		CalendarID: calendar.ExternalID,
		Managed:    calendar.Managed,
	}, nil
}

// credentialOf resolves the connector for a connection's provider and
// decodes the stored credential with that connector's own decoder.
func (b *Binder) credentialOf(connection storage.Connection) (provider.Connector, provider.Credential, error) {
	connector, err := b.registry.Connector(provider.ID(connection.Provider))
	if err != nil {
		return nil, nil, fmt.Errorf("connection %s: %w", connection.ID, err)
	}

	decoder, err := provider.DecoderOf(connector)
	if err != nil {
		return nil, nil, fmt.Errorf("connection %s: %w", connection.ID, err)
	}

	credential, err := decoder.DecodeCredential(connection.ID.String(), connection.Credentials)
	if err != nil {
		return nil, nil, fmt.Errorf("decode credential of connection %s: %w", connection.ID, err)
	}

	return connector, credential, nil
}
