// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav_test

import (
	"net/http"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

func TestConnector_DeletesAResourceWhoseNameHoldsTheSeparator(t *testing.T) {
	t.Parallel()

	// The symptom a user meets. A resource name is the server's to
	// choose, and the connector declares no instance writes, so it used
	// to refuse every id carrying the separator. A master named
	// "a#b.ics" could then not be deleted at all, on a reconcile or on a
	// teardown.
	const resourceName = "a#b.ics"

	server := newFakeServer().
		on(http.MethodDelete, "/20241234/calendars/home/"+resourceName, answer{status: http.StatusNoContent})

	eventID := provider.EventIDOf(resourceName)

	if err := newConnector(t, server).DeleteEvent(t.Context(), testCredential(t), homeCalendar, eventID); err != nil {
		t.Fatalf("DeleteEvent(%q) error = %v", eventID, err)
	}

	// The request is the only proof: a delete the connector refuses
	// leaves the calendar exactly as a delete it never sent does.
	deleted := server.last(t, http.MethodDelete)
	if want := "/20241234/calendars/home/" + resourceName; deleted.path != want {
		t.Errorf("the connector deleted %q, want %q", deleted.path, want)
	}
}
