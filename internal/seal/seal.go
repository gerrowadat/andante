// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package seal encrypts the few things andante has to be able to read back, under a
// key from --secret. Tokens themselves are looked up by hash. What's sealed is only
// there so that a URL can be shown to its owner again.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// MinSecretLength is the shortest --secret accepted.
const MinSecretLength = 32

// version leads each sealed value, so that the scheme can change later.
const version = 1

// ErrWrongKey means a value wasn't sealed with this key, or has been changed.
var ErrWrongKey = errors.New("this was not sealed with this secret")

// Box seals and opens values.
type Box struct {
	aead cipher.AEAD
}

// New derives a key from secret. The secret is stretched with HKDF and not a password
// hash, because it's meant to be long and random, as openssl rand -hex 32 makes.
func New(secret string) (*Box, error) {
	if len(secret) < MinSecretLength {
		return nil, fmt.Errorf("the secret must be at least %d characters", MinSecretLength)
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, "andante seal v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain. context ties the result to its purpose, and must be the same
// to open it, so that a value can't be moved to another field.
func (b *Box) Seal(plain []byte, context string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, plain, []byte(context)), nil
}

// Open decrypts what Seal returned, or returns ErrWrongKey.
func (b *Box) Open(sealed []byte, context string) ([]byte, error) {
	size := 1 + b.aead.NonceSize()
	if len(sealed) < size || sealed[0] != version {
		return nil, ErrWrongKey
	}
	plain, err := b.aead.Open(nil, sealed[1:size], sealed[size:], []byte(context))
	if err != nil {
		return nil, ErrWrongKey
	}
	return plain, nil
}
