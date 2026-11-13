// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"errors"
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

func TestICloud_DeclaresWhatWasMeasured(t *testing.T) {
	t.Parallel()

	if ICloud.Endpoint != "https://caldav.icloud.com/" {
		t.Errorf("endpoint is %q", ICloud.Endpoint)
	}
	if ICloud.Auth != AuthStyleBasic {
		t.Errorf("auth style is %s, want basic: Apple publishes no OAuth flow for calendars", ICloud.Auth)
	}
	if !ICloud.Quirks.NoFreeBusyReport {
		t.Error("the free/busy report was measured as refused with 400")
	}
	if !ICloud.Quirks.SyncReportsCollection {
		t.Error("a sync report lists the collection itself, which the connector has to drop")
	}
	if !ICloud.Quirks.IgnoresSyncLimit {
		t.Error("DAV:limit was measured as ignored")
	}
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
