// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"context"
	"testing"
)

// TestNewPool_InvalidConnString proves a malformed connection string fails
// fast, at parse time, with no network attempt: an empty string and a
// string with no recognizable scheme both fail the same way a real but
// unreachable host would, without needing one.
func TestNewPool_InvalidConnString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		connStr string
	}{
		{name: "not a connection string", connStr: "not a connection string"},
		{name: "invalid port", connStr: "postgres://user:pass@example.invalid:not-a-port/db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewPool(context.Background(), tt.connStr)
			if err == nil {
				t.Fatalf("NewPool(%q) error = nil, want an error", tt.connStr)
			}
		})
	}
}

// TestNewPoolWithCredentials_InvalidConnString mirrors
// TestNewPool_InvalidConnString, for NewPoolWithCredentials: a malformed
// database URL fails at pgxpool.ParseConfig, with no network attempt
// needed, the same shape as the existing test above.
func TestNewPoolWithCredentials_InvalidConnString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		databaseURL string
	}{
		{name: "not a connection string", databaseURL: "not a connection string"},
		{name: "invalid port", databaseURL: "postgres://example.invalid:not-a-port/db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewPoolWithCredentials(context.Background(), tt.databaseURL, "user", "password")
			if err == nil {
				t.Fatalf("NewPoolWithCredentials(%q) error = nil, want an error", tt.databaseURL)
			}
		})
	}
}

// TestNewStore_InvalidKey proves a wrong-length encryption key is
// rejected before any pool is touched, rather than failing later and
// confusingly on the first encrypt or decrypt call.
func TestNewStore_InvalidKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  []byte
	}{
		{name: "nil key", key: nil},
		{name: "empty key", key: []byte{}},
		{name: "short key", key: make([]byte, 16)},
		{name: "long key", key: make([]byte, 64)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewStore(nil, tt.key)
			if err == nil {
				t.Fatalf("NewStore with key of %d bytes: error = nil, want an error", len(tt.key))
			}
		})
	}
}
