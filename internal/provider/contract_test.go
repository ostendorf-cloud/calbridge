// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"errors"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

func TestInstanceIDOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		masterID   string
		occurrence string
		other      string
		want       bool
	}{
		{
			name:       "an id derived from the master matches it",
			masterID:   "standup.ics",
			occurrence: "20260406T090000",
			want:       true,
		},
		{
			name:       "the master's own id is not one of its instances",
			masterID:   "standup.ics",
			occurrence: "20260406T090000",
			other:      "standup.ics",
		},
		{
			name:       "an id of another event does not match",
			masterID:   "standup.ics",
			occurrence: "20260406T090000",
			other:      "retro.ics",
		},
		{
			name:       "an id sharing a prefix but not the separator does not match",
			masterID:   "standup.ics",
			occurrence: "20260406T090000",
			other:      "standup.ics.backup",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := provider.InstanceIDOf(tc.masterID, tc.occurrence)
			if tc.other != "" {
				id = tc.other
			}
			if got := provider.IsInstanceIDOf(id, tc.masterID); got != tc.want {
				t.Errorf("IsInstanceIDOf(%q, %q) = %t, want %t", id, tc.masterID, got, tc.want)
			}
		})
	}
}

func TestErrCredentialRejected_isNotARoutingMistake(t *testing.T) {
	t.Parallel()

	// A revoked app specific password and a withdrawn refresh token are
	// the account saying no, not calbridge handing a credential to the
	// wrong connector. A caller acts differently on each: one asks the
	// user to connect again, the other is a bug.
	if errors.Is(provider.ErrCredentialRejected, provider.ErrCredentialMismatch) {
		t.Error("a refused credential reads as one routed to the wrong provider")
	}
	if errors.Is(provider.ErrCredentialRejected, provider.ErrCredentialMissing) {
		t.Error("a refused credential reads as a missing one")
	}
}

func TestNormaliserOf(t *testing.T) {
	t.Parallel()

	t.Run("a connector that declares nothing gets one that changes nothing", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(provider.Capabilities{})

		normaliser, err := provider.NormaliserOf(connector)
		if err != nil {
			t.Fatalf("NormaliserOf() error = %v", err)
		}

		event := provider.Event{Summary: "Jour fixe"}
		got, err := normaliser.NormaliseEvent(event)
		if err != nil {
			t.Fatalf("NormaliseEvent() error = %v", err)
		}
		if got.Summary != event.Summary {
			t.Errorf("the event came back as %q, want it unchanged", got.Summary)
		}
	})

	t.Run("a connector that declares it and implements it gets its own", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(provider.Capabilities{NormalisesContent: true})
		connector.SetNormalisation(func(event provider.Event) provider.Event {
			event.Summary = "rewritten"

			return event
		})

		normaliser, err := provider.NormaliserOf(connector)
		if err != nil {
			t.Fatalf("NormaliserOf() error = %v", err)
		}

		got, err := normaliser.NormaliseEvent(provider.Event{Summary: "Jour fixe"})
		if err != nil {
			t.Fatalf("NormaliseEvent() error = %v", err)
		}
		if got.Summary != "rewritten" {
			t.Errorf("the connector's own normaliser was not used: got %q", got.Summary)
		}
	})

	t.Run("a connector that declares it and implements nothing is refused", func(t *testing.T) {
		t.Parallel()

		connector := declaresOnly{Connector: newFakeConnector(provider.Capabilities{})}

		if _, err := provider.NormaliserOf(connector); !errors.Is(err, provider.ErrInvalidCapabilities) {
			t.Errorf("NormaliserOf() error = %v, want ErrInvalidCapabilities", err)
		}
	})
}

// declaresOnly reports that it normalises content and implements no
// provider.ContentNormaliser. Embedding the interface rather than the
// fake is what leaves the method out.
type declaresOnly struct {
	provider.Connector
}

func (d declaresOnly) Capabilities() provider.Capabilities {
	return provider.Capabilities{NormalisesContent: true}
}

func TestDecoderOf(t *testing.T) {
	t.Parallel()

	t.Run("a connector that reads a stored credential answers one", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(provider.Capabilities{})

		decoder, err := provider.DecoderOf(connector)
		if err != nil {
			t.Fatalf("DecoderOf() error = %v", err)
		}

		cred, err := decoder.DecodeCredential(defaultConnection, []byte(defaultConnection))
		if err != nil {
			t.Fatalf("DecodeCredential() error = %v", err)
		}
		if cred.Provider() != fakeID {
			t.Errorf("credential names provider %q, want %q", cred.Provider(), fakeID)
		}
		if cred.Connection() != defaultConnection {
			t.Errorf("credential names connection %q, want %q", cred.Connection(), defaultConnection)
		}
	})

	t.Run("a connector that reads none is refused", func(t *testing.T) {
		t.Parallel()

		connector := declaresOnly{Connector: newFakeConnector(provider.Capabilities{})}

		if _, err := provider.DecoderOf(connector); !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("DecoderOf() error = %v, want ErrUnsupported", err)
		}
	})
}
