// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
	"github.com/ostendorf-cloud/calbridge/internal/provider/caldav"
)

func TestNewCredential_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		connection string
		profile    string
		account    string
		password   string
		want       error
	}{
		{
			name: "no connection", profile: "icloud", account: testAccount, password: testPassword,
			want: provider.ErrCredentialMissing,
		},
		{
			name: "no account", connection: "conn-1", profile: "icloud", password: testPassword,
			want: provider.ErrCredentialMissing,
		},
		{
			name: "no password", connection: "conn-1", profile: "icloud", account: testAccount,
			want: provider.ErrCredentialMissing,
		},
		{
			name: "a server there is no profile for", connection: "conn-1", profile: "fastmail",
			account: testAccount, password: testPassword, want: caldav.ErrUnknownProfile,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := caldav.NewCredential(tc.connection, tc.profile, tc.account, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), testPassword) {
				t.Fatal("the error carries the password")
			}
		})
	}
}

func TestCredential_CarriesNoPasswordIntoAMessage(t *testing.T) {
	t.Parallel()

	cred := testCredential(t)

	for _, rendered := range []string{
		cred.String(),
		fmt.Sprintf("%v", cred),
		fmt.Sprintf("%+v", cred),
		fmt.Sprint(cred),
	} {
		if strings.Contains(rendered, testPassword) {
			t.Fatalf("a rendered credential carries the password: %s", rendered)
		}
		if !strings.Contains(rendered, testAccount) {
			t.Errorf("a rendered credential names no account: %s", rendered)
		}
	}
}

func TestCredential_CarriesNoPasswordIntoALogLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	logger.Info("connection read", "credential", testCredential(t))

	if strings.Contains(out.String(), testPassword) {
		t.Fatalf("the log line carries the password: %s", out.String())
	}
	if !strings.Contains(out.String(), "icloud") {
		t.Errorf("the log line names no profile: %s", out.String())
	}
}

func TestCredential_CarriesNoPasswordIntoAnUnrelatedMarshal(t *testing.T) {
	t.Parallel()

	payload := struct {
		Credential caldav.Credential `json:"credential"`
	}{Credential: testCredential(t)}

	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), testPassword) {
		t.Fatalf("an unrelated marshal carries the password: %s", out)
	}
}

func TestEncodeCredential_RoundTrip(t *testing.T) {
	t.Parallel()

	raw, err := caldav.EncodeCredential(testCredential(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	back, err := caldav.DecodeCredential("conn-1", raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Account() != testAccount || back.Profile() != "icloud" || back.Connection() != "conn-1" {
		t.Errorf("decoded %+v", back)
	}
	if back.Provider() != caldav.ID {
		t.Errorf("decoded credential names provider %q", back.Provider())
	}
}

func TestDecodeCredential_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  error
	}{
		{name: "bytes that are not json", input: "not json", want: caldav.ErrMalformedCredential},
		{
			name:  "a credential with no password",
			input: `{"profile":"icloud","account":"anna.beispiel@example.com"}`,
			want:  provider.ErrCredentialMissing,
		},
		{
			name:  "a credential for a server there is no profile for",
			input: `{"profile":"fastmail","account":"anna.beispiel@example.com","password":"x"}`,
			want:  caldav.ErrUnknownProfile,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := caldav.DecodeCredential("conn-1", []byte(tc.input))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "password") && strings.Contains(err.Error(), tc.input) {
				t.Fatal("the error quotes the credential it could not read")
			}
		})
	}
}

func TestCredential_AuthenticatesEveryRequest(t *testing.T) {
	t.Parallel()

	server := discoverable(t).
		on("PROPFIND", "/20241234/calendars/", multistatus(recordedBody(t, "calendars.xml")))

	if _, err := newConnector(t, server).Calendars(t.Context(), testCredential(t)); err != nil {
		t.Fatalf("list calendars: %v", err)
	}

	calls := server.recorded()
	if len(calls) == 0 {
		t.Fatal("no request was made")
	}
	for i, c := range calls {
		if !strings.HasPrefix(c.header.Get("Authorization"), "Basic ") {
			t.Errorf("request %d carries no basic credential", i)
		}
		if strings.Contains(c.body, testPassword) {
			t.Errorf("request %d carries the password in its body", i)
		}
	}
}
