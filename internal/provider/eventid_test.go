// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// nativeIDs are the shapes a provider's own identifier comes in where the
// separator and the escape are concerned: neither byte, each of them, and
// both. A provider id is opaque, so every one of them has to survive the
// trip into an Event.ID and back.
var nativeIDs = map[string]string{
	"neither the separator nor the escape": "standup.ics",
	"the separator":                        "a#b.ics",
	"the escape":                           "a%b.ics",
	"both":                                 "a%23b.ics",
	"only the escape":                      "%",
	"only the separator":                   "#",
	"nothing at all":                       "",
	"bytes that are no valid utf-8":        "a\xffb#c.ics",
}

func TestEventIDOf_roundTripsAProviderIDOfAnyShape(t *testing.T) {
	t.Parallel()

	for name, nativeID := range nativeIDs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			eventID := provider.EventIDOf(nativeID)

			back, err := provider.NativeIDOf(eventID)
			if err != nil {
				t.Fatalf("NativeIDOf(%q) error = %v", eventID, err)
			}
			if back != nativeID {
				t.Errorf("NativeIDOf(EventIDOf(%q)) = %q", nativeID, back)
			}
			if strings.Contains(eventID, provider.InstanceSeparator) {
				t.Errorf("the Event.ID %q of a plain event carries the separator", eventID)
			}
		})
	}
}

func TestEventIDOf_tellsTwoProviderIDsApart(t *testing.T) {
	t.Parallel()

	// The escaping is only worth anything if it is injective: a resource
	// named "a#b.ics" and one named "a%23b.ics" are two objects, and an
	// id space that maps them onto one value loses an event.
	seen := map[string]string{}
	for _, nativeID := range nativeIDs {
		eventID := provider.EventIDOf(nativeID)
		if other, clash := seen[eventID]; clash {
			t.Errorf("%q and %q both become the Event.ID %q", other, nativeID, eventID)
		}
		seen[eventID] = nativeID
	}
}

func TestNativeIDOf_refusesAnIDItCannotHaveProduced(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"an id derived for an occurrence names no event of its own": provider.InstanceIDOf("a.ics", "20260304T090000"),
		"an unescaped separator":                                    "a#b.ics",
		"the escape outside an escape sequence":                     "a%2Gb.ics",
		"the escape at the end of the id":                           "a.ics%",
	}

	for name, eventID := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := provider.NativeIDOf(eventID); !errors.Is(err, provider.ErrInvalidEvent) {
				t.Errorf("NativeIDOf(%q) error = %v, want ErrInvalidEvent", eventID, err)
			}
		})
	}
}

func TestCutInstanceID_readsBothHalvesOfADerivedID(t *testing.T) {
	t.Parallel()

	for name, nativeID := range nativeIDs {
		if nativeID == "" {
			// A master with no id of its own is no master, and
			// Event.Validate refuses the event before an id is derived
			// from it.
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			masterID := provider.EventIDOf(nativeID)
			// The occurrence is as opaque as the id, so it is put through
			// the same shapes rather than being a timestamp here.
			occurrence := nativeID + "#20260304T090000"

			derived := provider.InstanceIDOf(masterID, occurrence)

			gotMaster, gotOccurrence, ok := provider.CutInstanceID(derived)
			if !ok {
				t.Fatalf("CutInstanceID(%q) reads no derived id", derived)
			}
			if gotMaster != masterID {
				t.Errorf("CutInstanceID(%q) master = %q, want %q", derived, gotMaster, masterID)
			}
			if gotOccurrence != occurrence {
				t.Errorf("CutInstanceID(%q) occurrence = %q, want %q", derived, gotOccurrence, occurrence)
			}
			if !provider.IsInstanceIDOf(derived, masterID) {
				t.Errorf("%q is not an instance id of its own master %q", derived, masterID)
			}
		})
	}
}

func TestCutInstanceID_readsNoDerivedIDInAPlainOne(t *testing.T) {
	t.Parallel()

	for name, nativeID := range nativeIDs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			eventID := provider.EventIDOf(nativeID)

			if master, occurrence, ok := provider.CutInstanceID(eventID); ok {
				t.Errorf("CutInstanceID(%q) reads the plain id as occurrence %q of %q", eventID, occurrence, master)
			}
		})
	}
}

func TestCutInstanceID_readsNoDerivedIDWhereTheMasterHalfDoesNotDecode(t *testing.T) {
	t.Parallel()

	// The master half is what this function exists to recover, so it is
	// an Event.ID a connector can act on or it is no derived id at all.
	// InstanceIDOf is handed a master EventIDOf produced, which carries
	// the escape byte only inside an escape sequence, so nothing it
	// derives is refused here.
	unreadable := "a%" + provider.InstanceSeparator + "b"

	master, occurrence, derived := provider.CutInstanceID(unreadable)
	if derived {
		t.Fatalf("CutInstanceID(%q) reads occurrence %q of the master %q, which NativeIDOf refuses",
			unreadable, occurrence, master)
	}
	if _, err := provider.NativeIDOf(master); master != "" && err != nil {
		t.Errorf("CutInstanceID(%q) returned the master %q, which NativeIDOf refuses", unreadable, master)
	}
}

func TestIsInstanceIDOf_answersNoForAnEventOfItsOwn(t *testing.T) {
	t.Parallel()

	// This is the collision the escaping closes. A CalDAV resource named
	// "a%23b.ics" reaches the connector as the native id "a#b.ics", and
	// read as a derived id it names an occurrence of a master "a" that
	// has nothing to do with it. One deletion of that master used to take
	// the unrelated event with it.
	plain := provider.EventIDOf("a" + provider.InstanceSeparator + "b.ics")

	if provider.IsInstanceIDOf(plain, "a") {
		t.Errorf("the event %q reads as an occurrence of the master \"a\"", plain)
	}
}

func TestIsInstanceIDOf_answersOnlyForAnIDItCouldHaveDerived(t *testing.T) {
	t.Parallel()

	// It answers on the whole id and not on a prefix of it, so an id
	// InstanceIDOf cannot have produced belongs to no master. The caller
	// turns a true answer into a deletion, and an id nothing derived
	// names no object such a deletion may reach.
	unreadable := "a" + provider.InstanceSeparator + "b%"

	if _, _, derived := provider.CutInstanceID(unreadable); derived {
		t.Fatalf("CutInstanceID(%q) reads an id EventIDOf cannot have produced", unreadable)
	}
	if provider.IsInstanceIDOf(unreadable, "a") {
		t.Errorf("%q reads as an occurrence of the master \"a\"", unreadable)
	}
}

func TestConnector_DeletingAMasterLeavesAnEventWhoseNativeIDHoldsTheSeparator(t *testing.T) {
	t.Parallel()

	// One calendar, a master named "a" with an override inside its own
	// object, and an unrelated event the server named "a#b.ics". The
	// deletion of the master takes its object, which is the master and
	// the override, and nothing else.
	connector := newFakeConnector(provider.Capabilities{NativeRecurrence: true})
	cred := credentialFor(defaultConnection)
	calendarID := primaryID(t, connector)

	master := provider.Event{
		Summary:    "Jour fixe",
		Start:      provider.Zoned(time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:        provider.Zoned(time.Date(2026, time.March, 4, 10, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Recurrence: provider.Recurrence{Rule: []string{"RRULE:FREQ=WEEKLY;COUNT=3"}},
	}
	master.ID = provider.EventIDOf("a")

	override := provider.Event{
		Summary: "Jour fixe, verlegt",
		Start:   provider.Zoned(time.Date(2026, time.March, 11, 14, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:     provider.Zoned(time.Date(2026, time.March, 11, 15, 0, 0, 0, time.UTC), "Europe/Berlin"),
		Recurrence: provider.Recurrence{
			MasterID:      master.ID,
			OriginalStart: provider.Zoned(time.Date(2026, time.March, 11, 9, 0, 0, 0, time.UTC), "Europe/Berlin"),
		},
	}
	override.ID = provider.InstanceIDOf(master.ID, "20260311T090000")

	plain := provider.Event{
		Summary: "Steuerberater",
		Start:   provider.Zoned(time.Date(2026, time.March, 5, 11, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:     provider.Zoned(time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
	plain.ID = provider.EventIDOf("a" + provider.InstanceSeparator + "b.ics")

	for _, event := range []provider.Event{master, override, plain} {
		if stored := connector.Put(calendarID, event); stored.ID != event.ID {
			t.Fatalf("the calendar stored %q as %q", event.ID, stored.ID)
		}
	}
	if got := len(connector.Events(calendarID)); got != 3 {
		t.Fatalf("calendar holds %d events, want the master, its override and the unrelated event", got)
	}

	if err := connector.DeleteEvent(t.Context(), cred, calendarID, master.ID); err != nil {
		t.Fatalf("DeleteEvent(%q) error = %v", master.ID, err)
	}

	if !connector.Holds(calendarID, plain.ID) {
		t.Errorf("deleting the master %q took the unrelated event %q with it", master.ID, plain.ID)
	}
	if connector.Holds(calendarID, override.ID) {
		t.Errorf("the override %q outlived the object it sits in", override.ID)
	}
	if got := len(connector.Events(calendarID)); got != 1 {
		t.Errorf("calendar holds %d events after the deletion, want only the unrelated one", got)
	}
}

func TestConnector_DeletesAMasterWhoseNativeIDHoldsTheSeparator(t *testing.T) {
	t.Parallel()

	// The symptom a user meets. A provider declaring no instance writes
	// refuses an id derived for an overridden occurrence, and a master
	// whose own name carries the separator used to be refused with it, so
	// a legitimate event could not be deleted at all.
	connector := newFakeConnector(provider.Capabilities{})
	cred := credentialFor(defaultConnection)
	calendarID := primaryID(t, connector)

	event := provider.Event{
		Summary: "Steuerberater",
		Start:   provider.Zoned(time.Date(2026, time.March, 5, 11, 0, 0, 0, time.UTC), "Europe/Berlin"),
		End:     provider.Zoned(time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC), "Europe/Berlin"),
	}
	event.ID = provider.EventIDOf("a" + provider.InstanceSeparator + "b.ics")
	if stored := connector.Put(calendarID, event); stored.ID != event.ID {
		t.Fatalf("the calendar stored %q as %q", event.ID, stored.ID)
	}

	if err := connector.DeleteEvent(t.Context(), cred, calendarID, event.ID); err != nil {
		t.Fatalf("DeleteEvent(%q) error = %v", event.ID, err)
	}

	if _, _, deletes := connector.Counts(); deletes != 1 {
		t.Errorf("the connector counted %d deletions, want the one that was asked for", deletes)
	}
	if connector.Holds(calendarID, event.ID) {
		t.Errorf("the event %q is still in the calendar", event.ID)
	}
}
