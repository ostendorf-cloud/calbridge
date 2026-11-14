// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/providertest"
)

// The in-memory connector these tests run against lives in
// internal/providertest, because the sync engine and the CalDAV
// connector test against the same one. A fake written once per consumer
// is one contract read several times, and the readings drift.
//
// The names below are this package's own words for it, so a test reads
// as a test of the interface rather than of another package.
type fakeConnector = providertest.Connector

const (
	// fakeID is the provider id the in-memory connector registers under.
	fakeID = providertest.ProviderID
	// defaultConnection is the connection the connector's first calendar
	// belongs to.
	defaultConnection = providertest.DefaultConnection
)

// credentialFor returns a credential for the in-memory connector, bound
// to one connection. One connector serves every connection of its
// provider, and the credential is what picks the account.
func credentialFor(connection string) providertest.Credential {
	return providertest.CredentialFor(connection)
}

// foreignCredential names another provider, which is what a credential
// routed to the wrong connector looks like.
type foreignCredential struct {
	provider   provider.ID
	connection string
}

func (c foreignCredential) Provider() provider.ID { return c.provider }

func (c foreignCredential) Connection() string { return c.connection }

// newFakeConnector returns a connector declaring the given capabilities,
// holding one writable calendar named "Primary" on the connection
// defaultConnection names.
func newFakeConnector(caps provider.Capabilities) *fakeConnector {
	c := providertest.New(caps)
	c.AddCalendar(defaultConnection, provider.Calendar{
		Name:     "Primary",
		TimeZone: providertest.DefaultZone,
		Primary:  true,
	})

	return c
}
