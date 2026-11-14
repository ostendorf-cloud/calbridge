// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider_test

import (
	"errors"
	"testing"
	"time"

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

func TestNormaliserOf_answersForEverythingAWriteChanges(t *testing.T) {
	t.Parallel()

	// A caller comparing a copy against what it wrote meets a field the
	// capabilities dropped and a field the format rewrote the same way. A
	// normaliser answering for one and not the other leaves the other
	// rewriting on every pass, which is the whole failure this interface
	// exists to prevent.
	connector := newFakeConnector(provider.Capabilities{NormalisesContent: true, Attendees: false})

	normaliser, err := provider.NormaliserOf(connector)
	if err != nil {
		t.Fatalf("NormaliserOf() error = %v", err)
	}

	event := provider.Event{
		Summary:   "Jour fixe",
		Start:     provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:       provider.Zoned(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Attendees: []provider.Attendee{{Email: "lena@example.com"}},
		Organizer: &provider.Attendee{Email: "anna@example.com"},
	}

	declared, err := normaliser.NormaliseEvent(event)
	if err != nil {
		t.Fatalf("NormaliseEvent() error = %v", err)
	}

	stored, err := connector.CreateEvent(t.Context(), credentialFor(defaultConnection), primaryID(t, connector), event)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	if len(declared.Attendees) != len(stored.Attendees) {
		t.Errorf("the declaration reports %d attendees and the write stored %d",
			len(declared.Attendees), len(stored.Attendees))
	}
	if (declared.Organizer == nil) != (stored.Organizer == nil) {
		t.Errorf("the declaration reports organizer %v and the write stored %v", declared.Organizer, stored.Organizer)
	}
}

// primaryID returns the id of the calendar newFakeConnector builds.
func primaryID(t *testing.T, c *fakeConnector) string {
	t.Helper()

	calendars, err := c.Calendars(t.Context(), credentialFor(defaultConnection))
	if err != nil {
		t.Fatalf("Calendars() error = %v", err)
	}
	if len(calendars) == 0 {
		t.Fatal("the connector holds no calendar")
	}

	return calendars[0].ID
}

func TestConnector_RefusesEveryWriteOfAnInstanceId(t *testing.T) {
	t.Parallel()

	// The interface says a connector declaring no instance writes returns
	// ErrUnsupported for an id IsInstanceIDOf matches. A fake that takes
	// one lets a caller pass a test the real connector fails, which is how
	// a capability check missing from a sweep stays invisible.
	connector := newFakeConnector(provider.Capabilities{})
	cred := credentialFor(defaultConnection)
	calendarID := primaryID(t, connector)
	instance := provider.InstanceIDOf("evt-1", "20260304T090000")

	t.Run("update", func(t *testing.T) {
		t.Parallel()

		event := provider.Event{
			ID:    instance,
			Start: provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
			End:   provider.Zoned(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		}

		_, err := connector.UpdateEvent(t.Context(), cred, calendarID, event)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("UpdateEvent() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()

		err := connector.DeleteEvent(t.Context(), cred, calendarID, instance)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("DeleteEvent() error = %v, want ErrUnsupported", err)
		}
	})
}

func TestConnector_ReplaceRefusesAnEventWithNoId(t *testing.T) {
	t.Parallel()

	// Putting such an event adds a second one instead of changing the
	// first, and a probe that counts nothing still passes.
	connector := newFakeConnector(provider.Capabilities{})
	calendarID := primaryID(t, connector)

	if _, err := connector.Replace(calendarID, provider.Event{Summary: "Jour fixe"}); !errors.Is(err, provider.ErrUnknownEvent) {
		t.Errorf("Replace() error = %v, want ErrUnknownEvent", err)
	}
	if got := len(connector.Events(calendarID)); got != 0 {
		t.Errorf("the refused replace stored %d events", got)
	}
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
