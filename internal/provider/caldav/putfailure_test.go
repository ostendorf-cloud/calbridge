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

// A failed write names the object by the resource name the server knows
// it under, not by the Event.ID the caller handed in. The two differ for
// every name holding the separator, and an operator reading the log has
// only the server's own name to match against.
//
// The resource here is named with the separator for that reason: with
// the id reported instead, both messages below would name "a%23b.ics",
// which appears in no server log and no collection listing.
const putFailureResource = "a#b.ics"

// putFailureEvent is the event each case below writes.
func putFailureEvent(t *testing.T) provider.Event {
	t.Helper()

	return provider.Event{
		ID:      provider.EventIDOf(putFailureResource),
		Summary: "Probe Hauptbuehne",
		Start:   provider.Zoned(time.Date(2026, time.March, 1, 18, 0, 0, 0, time.UTC), "UTC"),
		End:     provider.Zoned(time.Date(2026, time.March, 1, 22, 30, 0, 0, time.UTC), "UTC"),
	}
}

// putFailureServer answers the read an update makes and then refuses the
// write with status.
func putFailureServer(status int) *fakeServer {
	held := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Apple Inc.//iOS 18.1//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:a#b\r\nDTSTAMP:20260228T101500Z\r\n" +
		"CREATED:20260101T090000Z\r\nDTSTART:20260301T180000Z\r\nDTEND:20260301T223000Z\r\n" +
		"SUMMARY:Probe Hauptbuehne\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	return newFakeServer().
		on(http.MethodGet, "/20241234/calendars/home/"+putFailureResource, read(held, `"C=12@U=20241234/1"`)).
		on(http.MethodPut, "/20241234/calendars/home/"+putFailureResource, answer{status: status})
}

func TestConnector_AWriteRefusedByTheEntityTagNamesTheResource(t *testing.T) {
	t.Parallel()

	event := putFailureEvent(t)

	_, err := newConnector(t, putFailureServer(http.StatusPreconditionFailed)).
		UpdateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if !errors.Is(err, caldav.ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), putFailureResource) {
		t.Errorf("the refusal reads %q, want the resource name %q in it", err, putFailureResource)
	}
	if strings.Contains(err.Error(), event.ID) {
		t.Errorf("the refusal reads %q, want the resource name rather than the event id %q", err, event.ID)
	}
}

func TestConnector_AWriteOfAResourceTheServerLostNamesTheResource(t *testing.T) {
	t.Parallel()

	event := putFailureEvent(t)

	_, err := newConnector(t, putFailureServer(http.StatusNotFound)).
		UpdateEvent(t.Context(), testCredential(t), homeCalendar, event)
	if !errors.Is(err, provider.ErrUnknownEvent) {
		t.Fatalf("got %v, want ErrUnknownEvent", err)
	}
	if !strings.Contains(err.Error(), putFailureResource) {
		t.Errorf("the refusal reads %q, want the resource name %q in it", err, putFailureResource)
	}
	if strings.Contains(err.Error(), event.ID) {
		t.Errorf("the refusal reads %q, want the resource name rather than the event id %q", err, event.ID)
	}
}
