// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package password hashes and checks passwords, following OWASP and NIST SP 800-63B:
// Argon2id, a length limit, no composition rules, and a list of common passwords.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed" // For the list of common passwords.
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// What a password has to be.
const (
	MinLength = 12
	MaxLength = 128
)

const (
	saltBytes = 16
	hashBytes = 32
)

// Errors for a password that can't be used. The text is shown to the person typing it.
var (
	ErrTooShort = fmt.Errorf("a password needs at least %d characters", MinLength)
	ErrTooLong  = fmt.Errorf("a password can have at most %d characters", MaxLength)
	ErrCommon   = errors.New("that password is too easy to guess")
)

var errBadHash = errors.New("this is not a hash this package made")

//go:embed common.txt
var commonList string

var common = func() map[string]bool {
	set := map[string]bool{}
	for line := range strings.Lines(commonList) {
		set[strings.TrimSpace(line)] = true
	}
	return set
}()

// Check says whether a password can be used by someone called name.
func Check(password, name string) error {
	length := utf8.RuneCountInString(password)
	switch {
	case length < MinLength:
		return ErrTooShort
	case length > MaxLength:
		return ErrTooLong
	}
	lower := strings.ToLower(password)
	if common[lower] || strings.Contains(lower, strings.ToLower(name)) || sameCharacter(lower) {
		return ErrCommon
	}
	return nil
}

func sameCharacter(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	return strings.Trim(s, string(first)) == ""
}

// Params are how hard a hash is to compute. They're stored with each hash, so raising
// them leaves existing passwords working.
type Params struct {
	MemoryKiB uint32
	Passes    uint32
}

// DefaultParams are 64 MiB and 3 passes, which is above OWASP's minimum.
var DefaultParams = Params{MemoryKiB: 64 * 1024, Passes: 3}

// Hasher does the hashing. Each hash takes MemoryKiB, so it runs a few at a time, and
// logging in can't be used to run the server out of memory.
type Hasher struct {
	params Params
	slots  chan struct{}
	dummy  string
}

// NewHasher returns a Hasher that does at most concurrency hashes at once.
func NewHasher(params Params, concurrency int) (*Hasher, error) {
	h := &Hasher{params: params, slots: make(chan struct{}, concurrency)}
	dummy, err := h.Hash(context.Background(), "a password that matches nothing")
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait to hash a password: %w", ctx.Err())
	}
}

func (h *Hasher) release() { <-h.slots }

// normalise means the same password typed on a different keyboard is the same bytes.
func normalise(password string) []byte { return []byte(norm.NFC.String(password)) }

// Hash returns password's hash in the PHC string format.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	key := argon2.IDKey(normalise(password), salt, h.params.Passes, h.params.MemoryKiB, 1, hashBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=1$%s$%s", argon2.Version, h.params.MemoryKiB, h.params.Passes,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded.
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	var (
		version int
		params  Params
		lanes   uint8
	)
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.MemoryKiB, &params.Passes, &lanes); err != nil {
		return false, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := argon2.IDKey(normalise(password), salt, params.Passes, params.MemoryKiB, lanes, uint32(len(want))) //nolint:gosec // We wrote it.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Waste does the work of a Verify and fails, for a login with no real hash to check.
// Without it, a name that doesn't exist answers faster than one that does.
func (h *Hasher) Waste(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, password, h.dummy)
	return err
}
