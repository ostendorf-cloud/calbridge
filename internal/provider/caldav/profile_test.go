// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

func TestProfiles_AreComplete(t *testing.T) {
	t.Parallel()

	if len(Profiles) == 0 {
		t.Fatal("no profile is shipped")
	}
	for name, p := range Profiles {
		if name != p.Name {
			t.Errorf("profile keyed %q names itself %q", name, p.Name)
		}
		if err := p.Validate(); err != nil {
			t.Errorf("profile %q: %v", name, err)
		}
		if err := provider.ID(p.Name).Validate(); err != nil {
			t.Errorf("profile name %q does not have the shape of a provider id: %v", name, err)
		}
	}
}

func TestProfile_Validate(t *testing.T) {
	t.Parallel()

	valid := func() Profile {
		return Profile{
			Name:         "example",
			Endpoint:     "https://dav.example.com/",
			Discovery:    DiscoveryPrincipal,
			Auth:         AuthStyleBasic,
			HostSuffixes: []string{".example.com"},
		}
	}

	cases := []struct {
		name  string
		build func(Profile) Profile
	}{
		{name: "no name", build: func(p Profile) Profile { p.Name = ""; return p }},
		{name: "no endpoint", build: func(p Profile) Profile { p.Endpoint = ""; return p }},
		{name: "an endpoint that is not https", build: func(p Profile) Profile {
			p.Endpoint = "http://dav.example.com/"

			return p
		}},
		{name: "no discovery style", build: func(p Profile) Profile { p.Discovery = DiscoveryUnset; return p }},
		{name: "no authentication style", build: func(p Profile) Profile { p.Auth = AuthStyleUnset; return p }},
		{name: "no host suffix", build: func(p Profile) Profile { p.HostSuffixes = nil; return p }},
		{name: "a host suffix without its dot", build: func(p Profile) Profile {
			p.HostSuffixes = []string{"example.com"}

			return p
		}},
		{name: "push notifications CalDAV cannot deliver", build: func(p Profile) Profile {
			p.Capabilities.PushNotifications = true

			return p
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.build(valid()).Validate(); !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf("got %v, want ErrInvalidProfile", err)
			}
		})
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("a complete profile was refused: %v", err)
	}
}

func TestProfile_AllowsHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		host string
		want bool
	}{
		{name: "the endpoint itself", host: "caldav.icloud.com", want: true},
		{name: "the account's partition host", host: "p42-caldav.icloud.com", want: true},
		{name: "a partition host with its port", host: "p42-caldav.icloud.com:443", want: true},
		{name: "a host that only ends in the same words", host: "caldav.icloud.com.example.net", want: false},
		{name: "another host entirely", host: "collector.example.net", want: false},
		{name: "no host", host: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ICloud.allowsHost(tc.host); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQuirks_SteerTheRequestAndTheReading(t *testing.T) {
	t.Parallel()

	// A quirk earns its place by changing what the connector sends or
	// accepts. These are the two that do, read off a profile with the
	// quirk and one without it, so the table entry is what decides and
	// not a branch on a server name.
	honours := ICloud
	honours.Quirks.IgnoresSyncLimit = false
	honours.Quirks.SyncReportsCollection = false

	t.Run("a server that honours a limit is sent one", func(t *testing.T) {
		t.Parallel()

		withLimit, err := marshalBody(syncCollection{
			Level: "1",
			Limit: &limitProps{NResults: syncPageSize},
		})
		if err != nil {
			t.Fatalf("build the request body: %v", err)
		}
		if !strings.Contains(string(withLimit), "nresults") {
			t.Errorf("the body names no limit:\n%s", withLimit)
		}
	})

	t.Run("a server that lists the collection has that entry dropped", func(t *testing.T) {
		t.Parallel()

		calendar, err := url.Parse("https://p42-caldav.icloud.com/20241234/calendars/home/")
		if err != nil {
			t.Fatalf("parse the calendar url: %v", err)
		}
		ms := multistatus{
			SyncToken: "token-1",
			Responses: []davResp{{Href: "/20241234/calendars/home/"}},
		}

		page, err := readSyncPage(ICloud, calendar, ms)
		if err != nil {
			t.Fatalf("read the sync page: %v", err)
		}
		if len(page.changed) != 0 || len(page.deletes) != 0 {
			t.Errorf("the collection was read as an event resource: %+v", page)
		}

		if _, err := readSyncPage(honours, calendar, ms); !errors.Is(err, ErrServer) {
			t.Fatalf("got %v, want a profile without the quirk to report the entry it did not expect", err)
		}
	})
}

func TestProfileByName(t *testing.T) {
	t.Parallel()

	p, err := ProfileByName("icloud")
	if err != nil {
		t.Fatalf("look up the shipped profile: %v", err)
	}
	if p.Name != "icloud" {
		t.Errorf("got profile %q", p.Name)
	}

	if _, err := ProfileByName("fastmail"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("got %v, want ErrUnknownProfile", err)
	}
}
