// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// encryptionKeyBytes is the exact key length newEncryptor accepts: 32
// bytes, selecting AES-256 for the AES-GCM cipher every encryptor builds.
const encryptionKeyBytes = 32

// encryptor applies AES-256-GCM to every column this package stores
// encrypted at rest: connections.credentials and
// event_mapping.encrypted_details, per docs/concepts/calbridge.md.
// Repositories call encrypt before a write and decrypt after a read, so
// no caller above internal/storage/postgres ever has to remember to;
// see connections.go and event_mapping.go.
type encryptor struct {
	aead cipher.AEAD
}

// newEncryptor builds an encryptor from a raw key. The key is exactly
// encryptionKeyBytes long, decoded from hex by the caller
// (internal/config, following envconf's SecretOrFile/RequiredSecretOrFile
// convention the same way solobill's session and CSRF keys are, see
// internal/config's own doc comment): a key this package generated, not
// one typed by hand, so a wrong length is a deployment mistake to fail
// loudly on rather than stretch or truncate.
func newEncryptor(key []byte) (*encryptor, error) {
	if len(key) != encryptionKeyBytes {
		return nil, fmt.Errorf("encryption key: decodes to %d bytes, exactly %d are required", len(key), encryptionKeyBytes)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build AEAD: %w", err)
	}

	return &encryptor{aead: aead}, nil
}

// encrypt returns a nonce, generated fresh for every call, prepended to
// the AES-GCM sealed ciphertext of plaintext. A nil plaintext (no
// credential or no cached detail to store) stays nil, rather than
// encrypting zero bytes into a non-nil blob: the database column itself
// is nullable for exactly this case (event_mapping.encrypted_details),
// and a nil round-trips through decrypt back to nil.
func (e *encryptor) encrypt(plaintext []byte) ([]byte, error) {
	if plaintext == nil {
		return nil, nil
	}

	nonce := make([]byte, e.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return e.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt reverses encrypt: it splits the leading nonce off ciphertext
// and opens the remainder. A nil ciphertext stays nil; see encrypt's own
// comment.
func (e *encryptor) decrypt(ciphertext []byte) ([]byte, error) {
	if ciphertext == nil {
		return nil, nil
	}

	nonceSize := e.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext shorter than its own nonce")
	}

	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]

	plaintext, err := e.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	return plaintext, nil
}
