// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// renamed returns the in-memory connector under another provider id, so a
// registry test can hold more than one.
type renamed struct {
	*fakeConnector

	id provider.ID
}

func (r renamed) ID() provider.ID { return r.id }

func TestRegistryRegister(t *testing.T) {
	t.Parallel()

	t.Run("a registered connector is resolved by its id", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()
		connector := newFakeConnector(provider.Capabilities{PushNotifications: true})

		if err := registry.Register(connector); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		got, err := registry.Connector(fakeID)
		if err != nil {
			t.Fatalf("Connector() error = %v", err)
		}
		if got != provider.Connector(connector) {
			t.Errorf("Connector() returned another connector than the one registered")
		}
	})

	t.Run("the zero registry accepts a connector", func(t *testing.T) {
		t.Parallel()

		var registry provider.Registry
		if err := registry.Register(newFakeConnector(provider.Capabilities{})); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		if registry.Len() != 1 {
			t.Errorf("Len() = %d, want 1", registry.Len())
		}
	})

	t.Run("a second connector under a taken id is refused", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()
		if err := registry.Register(newFakeConnector(provider.Capabilities{})); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		err := registry.Register(newFakeConnector(provider.Capabilities{}))
		if !errors.Is(err, provider.ErrDuplicateProvider) {
			t.Errorf("Register() error = %v, want ErrDuplicateProvider", err)
		}
	})

	t.Run("a nil connector is refused", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()
		if err := registry.Register(nil); !errors.Is(err, provider.ErrNilConnector) {
			t.Errorf("Register() error = %v, want ErrNilConnector", err)
		}
	})

	t.Run("an id that is not well formed is refused", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name string
			id   provider.ID
		}{
			{name: "empty", id: ""},
			{name: "uppercase", id: "Google"},
			{name: "a space", id: "google calendar"},
			{name: "an underscore", id: "google_calendar"},
			{name: "a leading hyphen", id: "-google"},
			{name: "a trailing hyphen", id: "google-"},
			{name: "two hyphens in a row", id: "google--calendar"},
			{name: "a path separator", id: "google/calendar"},
			{name: "longer than the limit", id: provider.ID("a123456789b123456789c123456789d12")},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				registry := provider.NewRegistry()
				connector := renamed{fakeConnector: newFakeConnector(provider.Capabilities{}), id: tc.id}

				if err := registry.Register(connector); !errors.Is(err, provider.ErrInvalidID) {
					t.Errorf("Register(%q) error = %v, want ErrInvalidID", tc.id, err)
				}
				if registry.Len() != 0 {
					t.Errorf("Len() = %d, want the refused connector not to be held", registry.Len())
				}
			})
		}
	})

	t.Run("a well formed id is accepted", func(t *testing.T) {
		t.Parallel()

		for _, id := range []provider.ID{"google", "microsoft-graph", "caldav", "icloud2", "a", "a1234567890123456789012345678901"} {
			if err := id.Validate(); err != nil {
				t.Errorf("ID(%q).Validate() error = %v, want nil", id, err)
			}
		}
	})
}

func TestRegistryConnector(t *testing.T) {
	t.Parallel()

	t.Run("an unregistered id is unknown", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()

		_, err := registry.Connector("google")
		if !errors.Is(err, provider.ErrUnknownProvider) {
			t.Errorf("Connector() error = %v, want ErrUnknownProvider", err)
		}
	})

	t.Run("an unregistered id has no capabilities", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()

		caps, err := registry.Capabilities("google")
		if !errors.Is(err, provider.ErrUnknownProvider) {
			t.Errorf("Capabilities() error = %v, want ErrUnknownProvider", err)
		}
		if caps != (provider.Capabilities{}) {
			t.Errorf("Capabilities() = %+v, want the zero value", caps)
		}
	})

	t.Run("capabilities are read without holding the connector", func(t *testing.T) {
		t.Parallel()

		want := provider.Capabilities{
			PushNotifications: true,
			NativeRecurrence:  true,
			CalendarCreation:  true,
			Attendees:         true,
			FloatingTimes:     false,
		}

		registry := provider.NewRegistry()
		if err := registry.Register(newFakeConnector(want)); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		got, err := registry.Capabilities(fakeID)
		if err != nil {
			t.Fatalf("Capabilities() error = %v", err)
		}
		if got != want {
			t.Errorf("Capabilities() = %+v, want %+v", got, want)
		}
	})
}

func TestRegistryIDs(t *testing.T) {
	t.Parallel()

	t.Run("an empty registry lists nothing", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()
		if got := registry.IDs(); len(got) != 0 {
			t.Errorf("IDs() = %v, want an empty list", got)
		}
	})

	t.Run("ids come back sorted whatever the registration order", func(t *testing.T) {
		t.Parallel()

		registry := provider.NewRegistry()
		for _, id := range []provider.ID{"microsoft-graph", "caldav", "google"} {
			connector := renamed{fakeConnector: newFakeConnector(provider.Capabilities{}), id: id}
			if err := registry.Register(connector); err != nil {
				t.Fatalf("Register(%q) error = %v", id, err)
			}
		}

		want := []provider.ID{"caldav", "google", "microsoft-graph"}
		if got := registry.IDs(); !slices.Equal(got, want) {
			t.Errorf("IDs() = %v, want %v", got, want)
		}
	})
}

func TestRegistryConcurrentUse(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	ids := []provider.ID{"google", "microsoft-graph", "caldav", "memory"}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(2)

		go func() {
			defer wg.Done()

			connector := renamed{fakeConnector: newFakeConnector(provider.Capabilities{}), id: id}
			if err := registry.Register(connector); err != nil {
				t.Errorf("Register(%q) error = %v", id, err)
			}
		}()

		go func() {
			defer wg.Done()

			// The connector may or may not be registered yet. Either
			// answer is correct, and neither may race.
			if _, err := registry.Connector(id); err != nil && !errors.Is(err, provider.ErrUnknownProvider) {
				t.Errorf("Connector(%q) error = %v", id, err)
			}
			registry.IDs()
		}()
	}
	wg.Wait()

	if registry.Len() != len(ids) {
		t.Errorf("Len() = %d, want %d", registry.Len(), len(ids))
	}
}
