// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// Credential authenticates one CalDAV account: the profile naming the
// server, the account name, and the password that reaches it. For iCloud
// the account name is an Apple ID and the password is an app specific
// password the user generated, because Apple publishes no OAuth flow.
//
// Every field is unexported and no method returns the password, so the
// secret leaves this package only inside an Authorization header. The
// String and LogValue methods are what a formatted log line and a slog
// attribute reach, and both redact it.
type Credential struct {
	connection string
	profile    string
	account    string
	password   string
}

// NewCredential returns a credential for one connection against one
// profile. It is the only way to build one with a password in it.
func NewCredential(connection, profileName, account, password string) (Credential, error) {
	profile, err := ProfileByName(profileName)
	if err != nil {
		return Credential{}, err
	}
	switch {
	case connection == "":
		return Credential{}, fmt.Errorf("caldav credential names no connection: %w", provider.ErrCredentialMissing)
	case account == "":
		return Credential{}, fmt.Errorf("caldav credential for %q names no account: %w", profile.Name, provider.ErrCredentialMissing)
	case password == "":
		return Credential{}, fmt.Errorf("caldav credential for account %q carries no password: %w", account, provider.ErrCredentialMissing)
	}

	return Credential{connection: connection, profile: profile.Name, account: account, password: password}, nil
}

// Provider returns the connector this credential authenticates against.
func (c Credential) Provider() provider.ID { return ID }

// Connection returns the stored provider connection it was loaded from.
func (c Credential) Connection() string { return c.connection }

// Profile returns the name of the CalDAV server profile it reaches.
func (c Credential) Profile() string { return c.profile }

// Account returns the account name, which is not a secret: for iCloud it
// is the Apple ID the user typed.
func (c Credential) Account() string { return c.account }

// String renders the credential without its password, so a credential
// reaching a format directive or an error message carries no secret.
func (c Credential) String() string {
	return fmt.Sprintf("caldav credential for %s on %s (connection %s)", c.account, c.profile, c.connection)
}

// LogValue renders the credential for log/slog, without its password.
func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("profile", c.profile),
		slog.String("account", c.account),
		slog.String("connection", c.connection),
	)
}

// storedCredential is the serialised shape internal/storage holds as a
// connection's credentials. The storage layer encrypts those bytes
// before they reach the database and decrypts them on the way out, so
// this package hands over the plain form and nothing else.
type storedCredential struct {
	Profile  string `json:"profile"`
	Account  string `json:"account"`
	Password string `json:"password"`
}

// EncodeCredential serialises the credential for storage. It is named
// for the boundary it serves rather than implementing json.Marshaler, so
// a credential caught up in an unrelated json.Marshal of some larger
// value serialises to an empty object instead of to its password.
func EncodeCredential(cred Credential) ([]byte, error) {
	out, err := json.Marshal(storedCredential{
		Profile:  cred.profile,
		Account:  cred.account,
		Password: cred.password,
	})
	if err != nil {
		return nil, fmt.Errorf("encode caldav credential: %w", err)
	}

	return out, nil
}

// DecodeCredential implements provider.CredentialDecoder, which is what
// turns the bytes stored for a connection into the credential this
// connector authenticates with. The method is on the connector so a
// caller holding nothing but a provider.Connector can do it, and it
// returns the same value the package level function does.
func (c *Connector) DecodeCredential(connection string, raw []byte) (provider.Credential, error) {
	cred, err := DecodeCredential(connection, raw)
	if err != nil {
		// A typed nil behind the interface would read as a credential to
		// every caller that checks for one, so the failure returns none.
		return nil, err
	}

	return cred, nil
}

// DecodeCredential reads back what EncodeCredential wrote, for one
// connection. An error names the field that is missing and never the
// value of any of them.
func DecodeCredential(connection string, raw []byte) (Credential, error) {
	var stored storedCredential
	if err := json.Unmarshal(raw, &stored); err != nil {
		// The error from encoding/json can quote the input, which here is
		// a decrypted credential, so only the fact of the failure is kept.
		return Credential{}, fmt.Errorf("decode caldav credential for connection %q: %w", connection, ErrMalformedCredential)
	}

	return NewCredential(connection, stored.Profile, stored.Account, stored.Password)
}

// authorization returns the value of the Authorization header for this
// credential and the given profile.
func (c Credential) authorization(p Profile) (string, error) {
	if p.Auth != AuthStyleBasic {
		return "", fmt.Errorf("caldav profile %q authentication %s: %w", p.Name, p.Auth, provider.ErrUnsupported)
	}
	// RFC 7617 section 2 forbids a colon in the user-id of a Basic
	// credential, because the colon is what separates the two halves.
	if strings.Contains(c.account, ":") {
		return "", fmt.Errorf("caldav account name holds a colon, which basic authentication cannot carry: %w", provider.ErrCredentialMismatch)
	}

	return "Basic " + basicEncode(c.account, c.password), nil
}
