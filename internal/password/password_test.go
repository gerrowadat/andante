// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package password_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/password"
)

var cheap = password.Params{MemoryKiB: 8, Passes: 1}

const phrase = "a long enough phrase"

func hasher(t *testing.T, concurrency int) *password.Hasher {
	t.Helper()
	h, err := password.NewHasher(cheap, concurrency)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name, candidate string
		want            error
	}{
		{"fine", phrase, nil},
		{"shortest", strings.Repeat("ab", 6), nil},
		{"no composition rules", "alllowercaseletters", nil},
		{"too short", "abcdefghijk", password.ErrTooShort},
		{"counts characters, not bytes", "éééééé", password.ErrTooShort},
		{"longest", strings.Repeat("ab", 64), nil},
		{"too long", strings.Repeat("ab", 64) + "a", password.ErrTooLong},
		{"common", "PassWord1234", password.ErrCommon},
		{"has their name", "dave-dave-dave", password.ErrCommon},
		{"one character", strings.Repeat("z", 20), password.ErrCommon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := password.Check(tt.candidate, "dave"); !errors.Is(got, tt.want) {
				t.Errorf("Check = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashAndVerify(t *testing.T) {
	h := hasher(t, 2)
	ctx := t.Context()
	one, err := h.Hash(ctx, phrase)
	if err != nil {
		t.Fatal(err)
	}
	two, err := h.Hash(ctx, phrase)
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Error("two hashes of one password match, so the salt isn't random")
	}
	if !strings.HasPrefix(one, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Errorf("hash = %q", one)
	}
	if ok, err := h.Verify(ctx, phrase, one); err != nil || !ok {
		t.Errorf("right password: %v, %v", ok, err)
	}
	if ok, err := h.Verify(ctx, "the wrong phrase", one); err != nil || ok {
		t.Errorf("wrong password: %v, %v", ok, err)
	}
}

func TestOldHashesKeepWorking(t *testing.T) {
	encoded, err := hasher(t, 1).Hash(t.Context(), phrase)
	if err != nil {
		t.Fatal(err)
	}
	stronger, err := password.NewHasher(password.Params{MemoryKiB: 16, Passes: 2}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := stronger.Verify(t.Context(), phrase, encoded); err != nil || !ok {
		t.Errorf("Verify = %v, %v", ok, err)
	}
}

func TestComposedAndDecomposedAccentsMatch(t *testing.T) {
	h := hasher(t, 1)
	encoded, err := h.Hash(t.Context(), "café café café café")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.Verify(t.Context(), "café café café café", encoded); err != nil || !ok {
		t.Errorf("Verify = %v, %v", ok, err)
	}
}

func TestBadHashes(t *testing.T) {
	h := hasher(t, 1)
	for _, encoded := range []string{
		"", "plain", "$bcrypt$x$y$z$w", "$argon2id$v=1$m=8,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$nonsense$c2FsdA$aGFzaA", "$argon2id$v=19$m=8,t=1,p=1$!!$aGFzaA", "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$!!",
	} {
		if ok, err := h.Verify(t.Context(), "x", encoded); err == nil || ok {
			t.Errorf("Verify(%q) = %v, %v", encoded, ok, err)
		}
	}
}

func TestWaste(t *testing.T) {
	if err := hasher(t, 1).Waste(t.Context(), "anything"); err != nil {
		t.Errorf("Waste = %v", err)
	}
}

func TestHashesWaitTheirTurn(t *testing.T) {
	h := hasher(t, 1)
	encoded, err := h.Hash(t.Context(), phrase)
	if err != nil {
		t.Fatal(err)
	}
	// With the only slot taken, a caller gives up when its context does.
	defer password.Hold(h)()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Verify(ctx, phrase, encoded); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Verify = %v", err)
	}
	if _, err := h.Hash(ctx, phrase); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Hash = %v", err)
	}
}
