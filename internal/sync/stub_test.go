// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sync

import (
	"context"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// stubConnector declares capabilities and does nothing else. The tests
// in this file test the projection and the hash, which read the
// capability declaration and never call the provider.
type stubConnector struct {
	caps provider.Capabilities
}

func (c stubConnector) ID() provider.ID { return provider.ID("stub") }

func (c stubConnector) Capabilities() provider.Capabilities { return c.caps }

func (c stubConnector) Calendars(context.Context, provider.Credential) ([]provider.Calendar, error) {
	return nil, provider.ErrUnsupported
}

func (c stubConnector) CreateCalendar(context.Context, provider.Credential, string) (provider.Calendar, error) {
	return provider.Calendar{}, provider.ErrUnsupported
}

func (c stubConnector) DeleteCalendar(context.Context, provider.Credential, string) error {
	return provider.ErrUnsupported
}

func (c stubConnector) Changes(context.Context, provider.Credential, string, provider.SyncToken) (provider.ChangePage, error) {
	return provider.ChangePage{}, provider.ErrUnsupported
}

func (c stubConnector) CreateEvent(context.Context, provider.Credential, string, provider.Event) (provider.Event, error) {
	return provider.Event{}, provider.ErrUnsupported
}

func (c stubConnector) UpdateEvent(context.Context, provider.Credential, string, provider.Event) (provider.Event, error) {
	return provider.Event{}, provider.ErrUnsupported
}

func (c stubConnector) DeleteEvent(context.Context, provider.Credential, string, string) error {
	return provider.ErrUnsupported
}
