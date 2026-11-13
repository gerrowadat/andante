// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package password hashes and checks passwords, following OWASP and NIST SP 800-63B:
// Argon2id, a length limit, a list of common passwords, and composition rules only if
// an admin asks for them.
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
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// The limits on a Policy. MaxLength is fixed: it's well past NIST's 64, and it stops
// anyone handing the hasher a megabyte.
const (
	// Shortest is the least MinLength can be. It's NIST's floor.
	Shortest  = 8
	MaxLength = 128
	// Kinds is how many kinds of character there are: lower-case letters, capitals,
	// digits and everything else.
	Kinds = 4
)

// Policy is what a password has to be, on top of the checks that always apply.
type Policy struct {
	// MinLength is the fewest characters, from Shortest to MaxLength.
	MinLength int
	// Classes is how many of the Kinds of character it needs, from 0 to Kinds.
	Classes int
}

// DefaultPolicy is NIST SP 800-63B's: 15 characters for a password that can be the only
// factor, and no composition rules.
var DefaultPolicy = Policy{MinLength: 15}

// Errors for a password that can't be used. What Check returns wraps one of them, and its
// text is shown to the person typing it.
var (
	ErrTooShort  = errors.New("a password is too short")
	ErrTooLong   = fmt.Errorf("a password can have at most %d characters", MaxLength)
	ErrTooSimple = errors.New("a password needs more kinds of character")
	ErrCommon    = errors.New("that password is too easy to guess")
)

// ErrBadPolicy means a Policy is out of range.
var ErrBadPolicy = fmt.Errorf("a password's minimum length is %d to %d, and it can need 0 to %d kinds of character",
	Shortest, MaxLength, Kinds)

// rejection is an error with its own text for the person typing the password.
type rejection struct {
	kind error
	text string
}

func (r rejection) Error() string { return r.text }
func (r rejection) Unwrap() error { return r.kind }

const (
	saltBytes = 16
	hashBytes = 32
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

// Validate says whether p is in range.
func (p Policy) Validate() error {
	if p.MinLength < Shortest || p.MinLength > MaxLength || p.Classes < 0 || p.Classes > Kinds {
		return ErrBadPolicy
	}
	return nil
}

// Rule is p in a sentence, for a page that asks for a password.
func (p Policy) Rule() string {
	if p.Classes == 0 {
		return fmt.Sprintf("At least %d characters.", p.MinLength)
	}
	return fmt.Sprintf("At least %d characters, with at least %d of: lower-case letters, capitals, digits and anything else.",
		p.MinLength, p.Classes)
}

// Check says whether a password can be used by someone called name.
func (p Policy) Check(password, name string) error {
	length := utf8.RuneCountInString(password)
	switch {
	case length < p.MinLength:
		return rejection{ErrTooShort, fmt.Sprintf("a password needs at least %d characters", p.MinLength)}
	case length > MaxLength:
		return ErrTooLong
	case kinds(password) < p.Classes:
		return rejection{ErrTooSimple, fmt.Sprintf(
			"a password needs at least %d of: lower-case letters, capitals, digits and anything else", p.Classes)}
	}
	lower := strings.ToLower(password)
	if common[lower] || strings.Contains(lower, strings.ToLower(name)) || sameCharacter(lower) {
		return ErrCommon
	}
	return nil
}

// kinds counts the kinds of character in s. A letter with no case, as in most scripts
// that aren't Latin, Greek or Cyrillic, counts as anything else.
func kinds(s string) int {
	var lower, upper, digit, other int
	for _, r := range s {
		switch {
		case unicode.IsLower(r):
			lower = 1
		case unicode.IsUpper(r):
			upper = 1
		case unicode.IsDigit(r):
			digit = 1
		default:
			other = 1
		}
	}
	return lower + upper + digit + other
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
