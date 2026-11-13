// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"bytes"
	"testing"
)

func validKey() []byte {
	key := make([]byte, encryptionKeyBytes)
	for i := range key {
		key[i] = byte(i)
	}

	return key
}

func TestNewEncryptor_RejectsWrongKeyLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  []byte
	}{
		{name: "nil key", key: nil},
		{name: "empty key", key: []byte{}},
		{name: "short key", key: make([]byte, encryptionKeyBytes-1)},
		{name: "long key", key: make([]byte, encryptionKeyBytes+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := newEncryptor(tt.key); err == nil {
				t.Fatalf("newEncryptor(%d bytes): error = nil, want an error", len(tt.key))
			}
		})
	}
}

func TestEncryptor_EncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	enc, err := newEncryptor(validKey())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{name: "empty", plaintext: []byte{}},
		{name: "short", plaintext: []byte("hunter2")},
		{name: "long", plaintext: bytes.Repeat([]byte("a refresh token or a cached event body "), 50)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ciphertext, err := enc.encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}

			if bytes.Contains(ciphertext, tt.plaintext) && len(tt.plaintext) > 0 {
				t.Fatalf("ciphertext contains the plaintext in clear")
			}

			got, err := enc.decrypt(ciphertext)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}

			if !bytes.Equal(got, tt.plaintext) {
				t.Errorf("round trip = %q, want %q", got, tt.plaintext)
			}
		})
	}
}

// TestEncryptor_NilRoundTrips proves a nil plaintext, the shape
// connections.credentials being absent or event_mapping.encrypted_details
// being unset takes, stays nil through both encrypt and decrypt, rather
// than becoming a non-nil blob of zero bytes.
func TestEncryptor_NilRoundTrips(t *testing.T) {
	t.Parallel()

	enc, err := newEncryptor(validKey())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}

	ciphertext, err := enc.encrypt(nil)
	if err != nil {
		t.Fatalf("encrypt(nil): %v", err)
	}
	if ciphertext != nil {
		t.Fatalf("encrypt(nil) = %v, want nil", ciphertext)
	}

	plaintext, err := enc.decrypt(nil)
	if err != nil {
		t.Fatalf("decrypt(nil): %v", err)
	}
	if plaintext != nil {
		t.Fatalf("decrypt(nil) = %v, want nil", plaintext)
	}
}

func TestEncryptor_DecryptRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	enc, err := newEncryptor(validKey())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}

	ciphertext, err := enc.encrypt([]byte("a refresh token"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 0xFF

	if _, err := enc.decrypt(tampered); err == nil {
		t.Fatal("decrypt(tampered) = nil error, want authentication to fail")
	}
}

func TestEncryptor_DecryptRejectsShortCiphertext(t *testing.T) {
	t.Parallel()

	enc, err := newEncryptor(validKey())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}

	if _, err := enc.decrypt([]byte("too short")); err == nil {
		t.Fatal("decrypt(too short) = nil error, want it rejected as shorter than a nonce")
	}
}

// TestEncryptor_DifferentKeysCannotDecryptEachOther proves two
// encryptors built from different keys are genuinely independent: this
// is what makes the encryption key's own custody (see
// docs/concepts/calbridge.md's open questions) matter at all.
func TestEncryptor_DifferentKeysCannotDecryptEachOther(t *testing.T) {
	t.Parallel()

	keyA := validKey()
	keyB := validKey()
	keyB[0] ^= 0xFF

	encA, err := newEncryptor(keyA)
	if err != nil {
		t.Fatalf("newEncryptor(keyA): %v", err)
	}

	encB, err := newEncryptor(keyB)
	if err != nil {
		t.Fatalf("newEncryptor(keyB): %v", err)
	}

	ciphertext, err := encA.encrypt([]byte("a refresh token"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if _, err := encB.decrypt(ciphertext); err == nil {
		t.Fatal("decrypt with a different key succeeded, want it rejected")
	}
}
