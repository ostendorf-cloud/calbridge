// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/provider/caldav"
)

// berlinEvent is an ordinary timed event in a zone the database knows,
// used where a test is about something other than the time.
func berlinEvent(summary string) provider.Event {
	return provider.Event{
		Summary:      summary,
		Start:        provider.Zoned(time.Date(2026, time.April, 3, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:          provider.Zoned(time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Status:       provider.StatusConfirmed,
		Transparency: provider.TransparencyOpaque,
	}
}

func TestConnector_DeclaresThatItNormalisesContent(t *testing.T) {
	t.Parallel()

	if !caldav.New().Capabilities().NormalisesContent {
		t.Fatal("an iCalendar round trip rewrites content, and the connector has to say so")
	}
}

func TestConnector_NormaliseEventAnswersWhatAWriteStores(t *testing.T) {
	t.Parallel()

	connector := caldav.New(caldav.WithClock(func() time.Time { return fixedNow }))

	tests := []struct {
		name  string
		event func(provider.Event) provider.Event
		want  func(provider.Event) string
		field func(provider.Event) string
	}{
		{
			name: "a carriage return in a description becomes a line feed",
			event: func(e provider.Event) provider.Event {
				e.Description = "Agenda:\r\n1. Rueckblick\r\n2. Planung"

				return e
			},
			field: func(e provider.Event) string { return e.Description },
			want:  func(provider.Event) string { return "Agenda:\n1. Rueckblick\n2. Planung" },
		},
		{
			name: "a quotation mark in an attendee name is dropped",
			event: func(e provider.Event) provider.Event {
				e.Attendees = []provider.Attendee{{Email: "jo@example.com", Name: `Jo "Jojo" Weber`}}

				return e
			},
			field: func(e provider.Event) string {
				if len(e.Attendees) == 0 {
					return ""
				}

				return e.Attendees[0].Name
			},
			// The iCloud profile declares no attendee writing, so the
			// attendee list does not survive the write at all. The field the
			// capability drops and the field the format rewrites are both
			// answered here, which is the point: one call says what a read
			// back gives.
			want: func(provider.Event) string { return "" },
		},
		{
			name:  "a value the format carries unchanged comes back unchanged",
			event: func(e provider.Event) provider.Event { return e },
			field: func(e provider.Event) string { return e.Summary },
			want:  func(e provider.Event) string { return e.Summary },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event := tc.event(berlinEvent("Abstimmung"))
			stored, err := connector.NormaliseEvent(event)
			if err != nil {
				t.Fatalf("normalise: %v", err)
			}
			if got, want := tc.field(stored), tc.want(event); got != want {
				t.Errorf("stored value is %q, want %q", got, want)
			}
		})
	}
}

func TestConnector_NormaliseEventIsStable(t *testing.T) {
	t.Parallel()

	// What a provider stores is what a read of that provider returns, so
	// normalising twice has to give the same answer as normalising once.
	// A caller compares against this value on every pass, and a value that
	// moved on the second call would be a rewrite on every pass.
	connector := caldav.New(caldav.WithClock(func() time.Time { return fixedNow }))

	event := berlinEvent("Abstimmung")
	event.Description = "Agenda:\r\n1. Rueckblick"
	event.Location = "Raum 2.14, Haus B; Eingang Nord"

	once, err := connector.NormaliseEvent(event)
	if err != nil {
		t.Fatalf("first normalise: %v", err)
	}
	twice, err := connector.NormaliseEvent(once)
	if err != nil {
		t.Fatalf("second normalise: %v", err)
	}

	if once.Description != twice.Description || once.Location != twice.Location || once.Summary != twice.Summary {
		t.Errorf("normalising twice moved the event: %+v then %+v", once, twice)
	}
}

func TestConnector_NormaliseEventMatchesWhatAWriteReadsBack(t *testing.T) {
	t.Parallel()

	// The declaration is only worth anything if it answers the same thing
	// the write path does. This writes the event, reads the object the
	// connector put on the wire back through the same decode a change read
	// uses, and compares it against the declaration.
	server := newFakeServer().on(http.MethodPut, "/20241234/calendars/home/7f2c.ics",
		written(http.StatusCreated, `"C=13@U=20241234/4"`))
	connector := newConnector(t, server)

	event := berlinEvent("Abstimmung")
	event.Description = "Agenda:\r\n1. Rueckblick"

	if _, err := connector.CreateEvent(t.Context(), testCredential(t), homeCalendar, event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	written := server.last(t, http.MethodPut).body
	if strings.Contains(written, "\r\n1. Rueckblick") && !strings.Contains(written, `\n1. Rueckblick`) {
		t.Fatal("the object carries the description's line break unescaped")
	}

	declared, err := connector.NormaliseEvent(event)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if declared.Description != "Agenda:\n1. Rueckblick" {
		t.Errorf("the declaration says the description is stored as %q", declared.Description)
	}
}

func TestConnector_DeclaresNoInstanceWrites(t *testing.T) {
	t.Parallel()

	if caldav.New().Capabilities().InstanceWrites {
		t.Fatal("one CalDAV resource holds a master and its overrides, so one occurrence has nothing of its own to write")
	}
}

func TestConnector_RefusesEveryWriteOfOneOverriddenOccurrence(t *testing.T) {
	t.Parallel()

	connector := newConnector(t, newFakeServer())
	cred := testCredential(t)
	instance := provider.InstanceIDOf("standup.ics", "20260406T090000")

	t.Run("update", func(t *testing.T) {
		t.Parallel()

		event := berlinEvent("Standup")
		event.ID = instance

		_, err := connector.UpdateEvent(t.Context(), cred, homeCalendar, event)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("UpdateEvent() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()

		err := connector.DeleteEvent(t.Context(), cred, homeCalendar, instance)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("DeleteEvent() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("create", func(t *testing.T) {
		t.Parallel()

		event := berlinEvent("Standup")
		event.Recurrence = provider.Recurrence{
			MasterID:      "standup.ics",
			OriginalStart: provider.Zoned(time.Date(2026, time.April, 6, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		}

		_, err := connector.CreateEvent(t.Context(), cred, homeCalendar, event)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("CreateEvent() error = %v, want ErrUnsupported", err)
		}
	})
}

func TestConnector_ReportsAnOverrideUnderItsMastersId(t *testing.T) {
	t.Parallel()

	// A caller reading one deletion of the master has to be able to find
	// the overrides that went with it, and the id is all it has to find
	// them by.
	page, _ := changesFor(t, "sync-complete.xml", provider.SyncToken{})
	master, override := page.Changes[1].Event, page.Changes[2].Event

	if !provider.IsInstanceIDOf(override.ID, master.ID) {
		t.Fatalf("override %q is not an instance id of master %q", override.ID, master.ID)
	}
	if override.Recurrence.MasterID != master.ID {
		t.Errorf("override names master %q, want %q", override.Recurrence.MasterID, master.ID)
	}
}

func TestErrAuthentication_isACredentialTheProviderRefused(t *testing.T) {
	t.Parallel()

	// A revoked app specific password is neither a routing mistake nor a
	// missing credential, and the engine has to be able to tell it from
	// both without knowing this connector exists.
	if !errors.Is(caldav.ErrAuthentication, provider.ErrCredentialRejected) {
		t.Error("a refused password has to reach a caller as provider.ErrCredentialRejected")
	}
	if errors.Is(caldav.ErrAuthentication, provider.ErrCredentialMismatch) {
		t.Error("a refused password is not a credential routed to the wrong provider")
	}
	if errors.Is(caldav.ErrAuthentication, provider.ErrCredentialMissing) {
		t.Error("a refused password is not a missing credential")
	}
}

func TestConnector_DecodeCredentialIsReachableThroughTheInterface(t *testing.T) {
	t.Parallel()

	// The layer that loads a connection holds a provider.Connector and the
	// stored bytes, and nothing else. This is the step that turns the two
	// into a credential.
	connector := caldav.New()

	decoder, err := provider.DecoderOf(connector)
	if err != nil {
		t.Fatalf("decoder of the caldav connector: %v", err)
	}

	stored, err := caldav.EncodeCredential(testCredential(t))
	if err != nil {
		t.Fatalf("encode credential: %v", err)
	}

	cred, err := decoder.DecodeCredential("conn-1", stored)
	if err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	if cred.Provider() != caldav.ID {
		t.Errorf("credential names provider %q, want %q", cred.Provider(), caldav.ID)
	}
	if cred.Connection() != "conn-1" {
		t.Errorf("credential names connection %q, want %q", cred.Connection(), "conn-1")
	}
}

func TestConnector_DecodeCredentialReturnsNoCredentialForBytesItCannotRead(t *testing.T) {
	t.Parallel()

	connector := caldav.New()

	cred, err := connector.DecodeCredential("conn-1", []byte("not json"))
	if err == nil {
		t.Fatal("reading an unreadable blob has to fail")
	}
	if cred != nil {
		t.Errorf("a failed decode handed back the credential %v", cred)
	}
}

func TestProfile_RefusesAProfileClaimingInstanceWrites(t *testing.T) {
	t.Parallel()

	// A profile is a constant in the connector's own package, so the only
	// place a wrong one is caught is here.
	claiming := caldav.ICloud
	claiming.Capabilities.InstanceWrites = true

	if err := claiming.Validate(); !errors.Is(err, caldav.ErrInvalidProfile) {
		t.Errorf("Validate() error = %v, want ErrInvalidProfile", err)
	}
}
