// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// The in-memory connector is written against the exported surface only,
// the way a real connector in its own package is.
var _ provider.Connector = (*fakeConnector)(nil)

// fullCapabilities is a provider that can do everything the capability
// set names, which is the easy half of the engine's decisions.
func fullCapabilities() provider.Capabilities {
	return provider.Capabilities{
		PushNotifications: true,
		NativeRecurrence:  true,
		CalendarCreation:  true,
		Attendees:         true,
		FloatingTimes:     true,
	}
}

// primaryOf returns the connector's first calendar, which every test
// writes into.
func primaryOf(t *testing.T, c *fakeConnector, cred provider.Credential) provider.Calendar {
	t.Helper()

	calendars, err := c.Calendars(t.Context(), cred)
	if err != nil {
		t.Fatalf("Calendars() error = %v", err)
	}
	if len(calendars) == 0 {
		t.Fatal("Calendars() returned nothing, want at least the primary calendar")
	}

	return calendars[0]
}

// sprintReview is an event a sync engine would hand to a connector.
func sprintReview() provider.Event {
	return provider.Event{
		Summary:     "Sprint review",
		Description: "Demo and retrospective",
		Location:    "Room 2.14",
		Start:       provider.Zoned(wall(9, 30), "Europe/Berlin"),
		End:         provider.Zoned(wall(10, 30), "Europe/Berlin"),
		Marker:      provider.Marker{Edge: "edge-1", Source: "src-77"},
	}
}

func TestConnectorCredential(t *testing.T) {
	t.Parallel()

	t.Run("a credential for another provider is refused", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		other := fakeCredential{provider: "google", connection: "conn-1"}

		_, err := connector.Calendars(t.Context(), other)
		if !errors.Is(err, provider.ErrCredentialMismatch) {
			t.Errorf("Calendars() error = %v, want ErrCredentialMismatch", err)
		}
	})

	t.Run("a missing credential is refused", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())

		_, err := connector.Calendars(t.Context(), nil)
		if !errors.Is(err, provider.ErrCredentialMissing) {
			t.Errorf("Calendars() error = %v, want ErrCredentialMissing", err)
		}
	})

	t.Run("a cancelled context stops the call", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := connector.Calendars(ctx, credentialFor("conn-1"))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Calendars() error = %v, want context.Canceled", err)
		}
	})
}

func TestConnectorCalendars(t *testing.T) {
	t.Parallel()

	t.Run("a read-only calendar is listed and readable", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor(defaultConnection)
		shared := connector.addCalendar(defaultConnection, provider.Calendar{Name: "Shared", ReadOnly: true})

		page, err := connector.Changes(t.Context(), cred, shared.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() on a read-only calendar error = %v", err)
		}
		if len(page.Changes) != 0 {
			t.Errorf("Changes() returned %d changes, want none", len(page.Changes))
		}
	})

	t.Run("a read-only calendar refuses a write", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor(defaultConnection)
		shared := connector.addCalendar(defaultConnection, provider.Calendar{Name: "Shared", ReadOnly: true})

		_, err := connector.CreateEvent(t.Context(), cred, shared.ID, sprintReview())
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("CreateEvent() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("one connector serves several connections", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		second := connector.addCalendar("conn-2", provider.Calendar{Name: "Work"})

		first := primaryOf(t, connector, credentialFor(defaultConnection))
		if first.Name != "Primary" {
			t.Errorf("the first connection sees %q, want its own calendar", first.Name)
		}

		other := primaryOf(t, connector, credentialFor("conn-2"))
		if other.ID != second.ID {
			t.Errorf("the second connection sees %q, want %q", other.ID, second.ID)
		}
	})

	t.Run("a calendar of another connection is out of reach", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		theirs := connector.addCalendar("conn-2", provider.Calendar{Name: "Work"})

		_, err := connector.Changes(t.Context(), credentialFor(defaultConnection), theirs.ID, provider.SyncToken{})
		if !errors.Is(err, provider.ErrUnknownCalendar) {
			t.Errorf("Changes() error = %v, want ErrUnknownCalendar", err)
		}
	})

	t.Run("an unknown calendar is reported as unknown", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")

		_, err := connector.Changes(t.Context(), cred, "cal-does-not-exist", provider.SyncToken{})
		if !errors.Is(err, provider.ErrUnknownCalendar) {
			t.Errorf("Changes() error = %v, want ErrUnknownCalendar", err)
		}
	})
}

func TestConnectorCapabilityGates(t *testing.T) {
	t.Parallel()

	t.Run("a provider that cannot create a calendar says so", func(t *testing.T) {
		t.Parallel()

		caps := fullCapabilities()
		caps.CalendarCreation = false
		connector := newFakeConnector(caps)

		_, err := connector.CreateCalendar(t.Context(), credentialFor("conn-1"), "calbridge")
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("CreateCalendar() error = %v, want ErrUnsupported", err)
		}
		if connector.Capabilities().CalendarCreation {
			t.Error("Capabilities() reports calendar creation the connector refuses")
		}
	})

	t.Run("a provider that can create a calendar owns what it created", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())

		created, err := connector.CreateCalendar(t.Context(), credentialFor("conn-1"), "calbridge")
		if err != nil {
			t.Fatalf("CreateCalendar() error = %v", err)
		}
		if !created.Managed {
			t.Error("CreateCalendar() returned a calendar calbridge does not own")
		}
		if created.Name != "calbridge" {
			t.Errorf("CreateCalendar() name = %q, want calbridge", created.Name)
		}
	})

	t.Run("a provider that cannot create a calendar also cannot delete one", func(t *testing.T) {
		t.Parallel()

		caps := fullCapabilities()
		caps.CalendarCreation = false
		connector := newFakeConnector(caps)
		primary := primaryOf(t, connector, credentialFor("conn-1"))

		err := connector.DeleteCalendar(t.Context(), credentialFor("conn-1"), primary.ID)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("DeleteCalendar() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("a provider that can create a calendar removes what it created", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")

		created, err := connector.CreateCalendar(t.Context(), cred, "calbridge")
		if err != nil {
			t.Fatalf("CreateCalendar() error = %v", err)
		}
		if err := connector.DeleteCalendar(t.Context(), cred, created.ID); err != nil {
			t.Fatalf("DeleteCalendar() error = %v", err)
		}

		if _, err := connector.Changes(t.Context(), cred, created.ID, provider.SyncToken{}); !errors.Is(err, provider.ErrUnknownCalendar) {
			t.Errorf("Changes() on the deleted calendar error = %v, want ErrUnknownCalendar", err)
		}
	})

	t.Run("deleting a calendar that is already gone succeeds", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")

		created, err := connector.CreateCalendar(t.Context(), cred, "calbridge")
		if err != nil {
			t.Fatalf("CreateCalendar() error = %v", err)
		}
		if err := connector.DeleteCalendar(t.Context(), cred, created.ID); err != nil {
			t.Fatalf("DeleteCalendar() error = %v", err)
		}
		if err := connector.DeleteCalendar(t.Context(), cred, created.ID); err != nil {
			t.Errorf("DeleteCalendar() repeated error = %v, want nil so an interrupted deletion is safe to repeat", err)
		}
	})

	t.Run("deleting a calendar calbridge did not create is refused", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		err := connector.DeleteCalendar(t.Context(), cred, primary.ID)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("DeleteCalendar() error = %v, want ErrUnsupported, calbridge never removes a calendar it does not own", err)
		}

		calendars, err := connector.Calendars(t.Context(), cred)
		if err != nil {
			t.Fatalf("Calendars() error = %v", err)
		}
		if len(calendars) != 1 {
			t.Errorf("Calendars() returned %d calendars, want the primary one still there", len(calendars))
		}
	})

	t.Run("a provider without native recurrence refuses a rule", func(t *testing.T) {
		t.Parallel()

		caps := fullCapabilities()
		caps.NativeRecurrence = false
		connector := newFakeConnector(caps)
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		repeating := sprintReview()
		repeating.Recurrence = provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO"}}

		_, err := connector.CreateEvent(t.Context(), cred, primary.ID, repeating)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("CreateEvent() error = %v, want ErrUnsupported, so the engine expands the series instead", err)
		}
	})

	t.Run("a provider with native recurrence keeps the rule", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		repeating := sprintReview()
		repeating.Recurrence = provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO"}}

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, repeating)
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if !stored.Recurrence.IsMaster() {
			t.Error("CreateEvent() dropped the recurrence rule")
		}
	})

	t.Run("a provider without floating times refuses one", func(t *testing.T) {
		t.Parallel()

		caps := fullCapabilities()
		caps.FloatingTimes = false
		connector := newFakeConnector(caps)
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		floating := sprintReview()
		floating.Start = provider.Floating(wall(9, 30))
		floating.End = provider.Floating(wall(10, 30))

		_, err := connector.CreateEvent(t.Context(), cred, primary.ID, floating)
		if !errors.Is(err, provider.ErrUnsupported) {
			t.Errorf("CreateEvent() error = %v, want ErrUnsupported, so the engine anchors the time first", err)
		}
	})

	t.Run("a provider without attendees drops them instead of failing", func(t *testing.T) {
		t.Parallel()

		caps := fullCapabilities()
		caps.Attendees = false
		connector := newFakeConnector(caps)
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		invited := sprintReview()
		invited.Attendees = []provider.Attendee{
			{Email: "simon@example.com", Name: "Simon Ostendorf", Response: provider.ResponseAccepted},
		}

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, invited)
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if len(stored.Attendees) != 0 {
			t.Errorf("CreateEvent() kept %d attendees, want none", len(stored.Attendees))
		}
	})

	t.Run("a provider with attendees keeps them", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		invited := sprintReview()
		invited.Attendees = []provider.Attendee{
			{Email: "simon@example.com", Name: "Simon Ostendorf", Response: provider.ResponseAccepted},
		}

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, invited)
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if len(stored.Attendees) != 1 || stored.Attendees[0].Email != "simon@example.com" {
			t.Errorf("CreateEvent() attendees = %+v, want the one that went in", stored.Attendees)
		}
	})
}

func TestConnectorEventLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("a created event comes back with an id and its marker", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview())
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if stored.ID == "" {
			t.Error("CreateEvent() returned no id")
		}
		if stored.ETag == "" {
			t.Error("CreateEvent() returned no change tag")
		}
		if stored.Marker != (provider.Marker{Edge: "edge-1", Source: "src-77"}) {
			t.Errorf("CreateEvent() marker = %+v, want the one that went in", stored.Marker)
		}
		if stored.Created.IsZero() {
			t.Error("CreateEvent() left Created at its zero value, want the moment of the write")
		}
		if stored.Updated.IsZero() {
			t.Error("CreateEvent() left Updated at its zero value, want the moment of the write")
		}
	})

	t.Run("the organiser is dropped on write", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		organised := sprintReview()
		organised.Organizer = &provider.Attendee{Email: "lead@example.com", Name: "Team Lead"}

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, organised)
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if stored.Organizer != nil {
			t.Errorf("CreateEvent() organiser = %+v, want nil, every provider sets it from the writing account", stored.Organizer)
		}
	})

	t.Run("an invalid event never reaches the provider", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		broken := sprintReview()
		broken.End = provider.Zoned(wall(8, 30), "Europe/Berlin")

		if _, err := connector.CreateEvent(t.Context(), cred, primary.ID, broken); !errors.Is(err, provider.ErrInvalidEvent) {
			t.Errorf("CreateEvent() error = %v, want ErrInvalidEvent", err)
		}
	})

	t.Run("an update of an event the calendar does not hold is unknown", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		missing := sprintReview()
		missing.ID = "evt-does-not-exist"

		if _, err := connector.UpdateEvent(t.Context(), cred, primary.ID, missing); !errors.Is(err, provider.ErrUnknownEvent) {
			t.Errorf("UpdateEvent() error = %v, want ErrUnknownEvent", err)
		}
	})

	t.Run("an update changes the event and its change tag", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview())
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}

		stored.Summary = "Sprint review, moved"
		updated, err := connector.UpdateEvent(t.Context(), cred, primary.ID, stored)
		if err != nil {
			t.Fatalf("UpdateEvent() error = %v", err)
		}
		if updated.Summary != "Sprint review, moved" {
			t.Errorf("UpdateEvent() summary = %q, want the new one", updated.Summary)
		}
		if updated.ETag == stored.ETag {
			t.Error("UpdateEvent() left the change tag alone")
		}
		if !updated.Created.Equal(stored.Created) {
			t.Errorf("UpdateEvent() created = %v, want the original %v", updated.Created, stored.Created)
		}
		if updated.Updated.Before(stored.Updated) {
			t.Errorf("UpdateEvent() updated = %v, want no earlier than the previous write %v", updated.Updated, stored.Updated)
		}
	})

	t.Run("deleting an event that is already gone succeeds", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview())
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if err := connector.DeleteEvent(t.Context(), cred, primary.ID, stored.ID); err != nil {
			t.Fatalf("DeleteEvent() error = %v", err)
		}
		if err := connector.DeleteEvent(t.Context(), cred, primary.ID, stored.ID); err != nil {
			t.Errorf("DeleteEvent() repeated error = %v, want nil so an interrupted run is safe to repeat", err)
		}
	})
}

func TestConnectorChanges(t *testing.T) {
	t.Parallel()

	t.Run("an initial sync reports every event and no deletion", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		for range 3 {
			if _, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview()); err != nil {
				t.Fatalf("CreateEvent() error = %v", err)
			}
		}

		page, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if err := page.Validate(); err != nil {
			t.Fatalf("the page the connector returned is not one a caller can act on: %v", err)
		}
		if page.State != provider.PageStateComplete {
			t.Errorf("Changes() state = %s, want complete", page.State)
		}
		if len(page.Changes) != 3 {
			t.Fatalf("Changes() returned %d changes, want 3", len(page.Changes))
		}
		for _, c := range page.Changes {
			if c.Kind != provider.ChangeKindUpsert {
				t.Errorf("initial sync reported a %s, want every change to be an upsert", c.Kind)
			}
		}
		if !page.State.Storable() {
			t.Error("a complete page's token is not storable, so no run could ever save a position")
		}
	})

	t.Run("an incremental read reports what changed since the token", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		kept, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview())
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		first, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}

		added, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview())
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		if err := connector.DeleteEvent(t.Context(), cred, primary.ID, kept.ID); err != nil {
			t.Fatalf("DeleteEvent() error = %v", err)
		}

		second, err := connector.Changes(t.Context(), cred, primary.ID, first.Token)
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if err := second.Validate(); err != nil {
			t.Fatalf("the page the connector returned is not one a caller can act on: %v", err)
		}

		want := []provider.Change{
			{Kind: provider.ChangeKindUpsert, EventID: added.ID},
			{Kind: provider.ChangeKindDelete, EventID: kept.ID},
		}
		if len(second.Changes) != len(want) {
			t.Fatalf("Changes() returned %d changes, want %d", len(second.Changes), len(want))
		}
		for i, c := range second.Changes {
			if c.Kind != want[i].Kind || c.EventID != want[i].EventID {
				t.Errorf("change %d = %s of %q, want %s of %q", i, c.Kind, c.EventID, want[i].Kind, want[i].EventID)
			}
		}
	})

	t.Run("a deletion carries the id and no event", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		stored, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview())
		if err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		position, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if err := connector.DeleteEvent(t.Context(), cred, primary.ID, stored.ID); err != nil {
			t.Fatalf("DeleteEvent() error = %v", err)
		}

		page, err := connector.Changes(t.Context(), cred, primary.ID, position.Token)
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if len(page.Changes) != 1 {
			t.Fatalf("Changes() returned %d changes, want 1", len(page.Changes))
		}

		got := page.Changes[0]
		if got.Kind != provider.ChangeKindDelete {
			t.Fatalf("change kind = %s, want delete", got.Kind)
		}
		if got.Event.ID != "" || got.Event.Summary != "" {
			t.Errorf("a delete carried an event: %+v", got.Event)
		}
		if got.Event.Status != provider.StatusConfirmed {
			t.Error("a delete was reported as a cancelled event as well as a delete")
		}
	})

	t.Run("a token the provider rejected restarts from an initial sync", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		if _, err := connector.CreateEvent(t.Context(), cred, primary.ID, sprintReview()); err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
		position, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}

		connector.invalidate(position.Token)

		_, err = connector.Changes(t.Context(), cred, primary.ID, position.Token)
		if !errors.Is(err, provider.ErrSyncTokenInvalid) {
			t.Fatalf("Changes() error = %v, want ErrSyncTokenInvalid", err)
		}

		recovered, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() after dropping the token error = %v", err)
		}
		if len(recovered.Changes) != 1 {
			t.Errorf("the initial sync returned %d changes, want the one event the calendar holds", len(recovered.Changes))
		}
	})

	t.Run("a token the provider never issued is rejected", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		_, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{Value: "not a token"})
		if !errors.Is(err, provider.ErrSyncTokenInvalid) {
			t.Errorf("Changes() error = %v, want ErrSyncTokenInvalid", err)
		}
	})
}

func TestConnectorChangePaging(t *testing.T) {
	t.Parallel()

	// drain reads every page of a run and returns the changes in order,
	// plus the last page. It is what a sync run does with the contract.
	drain := func(t *testing.T, c *fakeConnector, cred provider.Credential, calendarID string, from provider.SyncToken) ([]provider.Change, provider.ChangePage) {
		t.Helper()

		var (
			all  []provider.Change
			page provider.ChangePage
			err  error
		)
		token := from
		for round := 0; ; round++ {
			if round > 100 {
				t.Fatal("the change read did not finish within 100 pages")
			}

			page, err = c.Changes(t.Context(), cred, calendarID, token)
			if err != nil {
				t.Fatalf("Changes() error = %v", err)
			}
			if err := page.Validate(); err != nil {
				t.Fatalf("the page the connector returned is not one a caller can act on: %v", err)
			}

			all = append(all, page.Changes...)
			if !page.State.More() {
				break
			}
			token = page.Token
		}

		return all, page
	}

	fill := func(t *testing.T, c *fakeConnector, cred provider.Credential, calendarID string, count int) {
		t.Helper()

		for range count {
			if _, err := c.CreateEvent(t.Context(), cred, calendarID, sprintReview()); err != nil {
				t.Fatalf("CreateEvent() error = %v", err)
			}
		}
	}

	t.Run("a paged initial sync hands over every event", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		connector.pageSize = 2
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)
		fill(t, connector, cred, primary.ID, 5)

		all, last := drain(t, connector, cred, primary.ID, provider.SyncToken{})
		if len(all) != 5 {
			t.Errorf("the run returned %d changes, want 5", len(all))
		}
		if last.State != provider.PageStateComplete {
			t.Errorf("the last page's state = %s, want complete", last.State)
		}
	})

	t.Run("a paged initial sync hands out no storable position until it finishes", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		connector.pageSize = 2
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)
		fill(t, connector, cred, primary.ID, 5)

		first, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if first.State != provider.PageStateContinued {
			t.Fatalf("the first page's state = %s, want continued", first.State)
		}
		if first.State.Storable() {
			t.Error("a continuation token reports itself as storable, so a crash would resume from a cursor that means nothing")
		}
		if !first.State.More() {
			t.Error("a continued page reports that the run is over")
		}
	})

	t.Run("a run cut short resumes from a resumable token", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		// Take a position first, so the pages below are incremental.
		position, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}

		connector.pageSize = 2
		connector.incrementalState = provider.PageStateResumable
		fill(t, connector, cred, primary.ID, 5)

		first, err := connector.Changes(t.Context(), cred, primary.ID, position.Token)
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if first.State != provider.PageStateResumable {
			t.Fatalf("the first page's state = %s, want resumable", first.State)
		}
		if !first.State.Storable() {
			t.Fatal("a resumable token reports itself as not storable, so a truncated read would cost a full sync")
		}

		// The run stops here and a later one starts from the stored
		// token. It must see the remaining changes and no repeat.
		rest, last := drain(t, connector, cred, primary.ID, first.Token)
		if len(first.Changes)+len(rest) != 5 {
			t.Errorf("the two runs returned %d changes together, want 5", len(first.Changes)+len(rest))
		}
		if last.State != provider.PageStateComplete {
			t.Errorf("the last page's state = %s, want complete", last.State)
		}
	})

	t.Run("a continued run delivers every change when it is read to the end", func(t *testing.T) {
		t.Parallel()

		connector := newFakeConnector(fullCapabilities())
		cred := credentialFor("conn-1")
		primary := primaryOf(t, connector, cred)

		position, err := connector.Changes(t.Context(), cred, primary.ID, provider.SyncToken{})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}

		connector.pageSize = 2
		connector.incrementalState = provider.PageStateContinued
		fill(t, connector, cred, primary.ID, 5)

		all, last := drain(t, connector, cred, primary.ID, position.Token)
		if len(all) != 5 {
			t.Errorf("the run returned %d changes, want 5", len(all))
		}
		if last.State != provider.PageStateComplete {
			t.Errorf("the last page's state = %s, want complete", last.State)
		}
		if !last.State.Storable() {
			t.Error("the run finished without a storable position")
		}
	})
}

func TestConnectorThroughRegistry(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	if err := registry.Register(newFakeConnector(fullCapabilities())); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	cred := credentialFor("conn-1")

	// A caller holds a provider id and a credential, and nothing else.
	connector, err := registry.Connector(cred.Provider())
	if err != nil {
		t.Fatalf("Connector() error = %v", err)
	}

	calendars, err := connector.Calendars(t.Context(), cred)
	if err != nil {
		t.Fatalf("Calendars() error = %v", err)
	}
	if len(calendars) != 1 {
		t.Fatalf("Calendars() returned %d calendars, want 1", len(calendars))
	}

	event := sprintReview()
	if connector.Capabilities().FloatingTimes {
		event.Start = provider.Floating(wall(9, 30))
		event.End = provider.Floating(wall(10, 30))
	}

	stored, err := connector.CreateEvent(t.Context(), cred, calendars[0].ID, event)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}
	if stored.Created.IsZero() {
		t.Error("CreateEvent() left Created at its zero value, want the moment of the write")
	}
	if stored.Created.After(time.Now()) {
		t.Error("CreateEvent() returned a creation time in the future")
	}

	page, err := connector.Changes(t.Context(), cred, calendars[0].ID, provider.SyncToken{})
	if err != nil {
		t.Fatalf("Changes() error = %v", err)
	}
	if len(page.Changes) != 1 || page.Changes[0].EventID != stored.ID {
		t.Errorf("Changes() = %+v, want the one event just written", page.Changes)
	}
}
