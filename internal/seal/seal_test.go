// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package seal_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/seal"
)

func box(t *testing.T, secret string) *seal.Box {
	t.Helper()
	b, err := seal.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := box(t, strings.Repeat("a", 32))
	sealed, err := b.Seal([]byte("a token"), "feed-token")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("a token")) {
		t.Error("the value is readable in what was sealed")
	}
	plain, err := b.Open(sealed, "feed-token")
	if err != nil || string(plain) != "a token" {
		t.Errorf("Open = %q, %v", plain, err)
	}
}

func TestEachSealIsDifferent(t *testing.T) {
	b := box(t, strings.Repeat("a", 32))
	one, _ := b.Seal([]byte("x"), "c")
	two, _ := b.Seal([]byte("x"), "c")
	if bytes.Equal(one, two) {
		t.Error("sealing twice gave the same bytes, so the nonce isn't random")
	}
}

func TestWhatOpenRefuses(t *testing.T) {
	b := box(t, strings.Repeat("a", 32))
	sealed, err := b.Seal([]byte("x"), "right")
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	wrongVersion := bytes.Clone(sealed)
	wrongVersion[0] = 9

	for name, try := range map[string]func() ([]byte, error){
		"another secret":  func() ([]byte, error) { return box(t, strings.Repeat("b", 32)).Open(sealed, "right") },
		"another context": func() ([]byte, error) { return b.Open(sealed, "wrong") },
		"tampered":        func() ([]byte, error) { return b.Open(tampered, "right") },
		"unknown version": func() ([]byte, error) { return b.Open(wrongVersion, "right") },
		"too short":       func() ([]byte, error) { return b.Open([]byte{1, 2}, "right") },
		"empty":           func() ([]byte, error) { return b.Open(nil, "right") },
	} {
		if _, err := try(); !errors.Is(err, seal.ErrWrongKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestShortSecretsAreRefused(t *testing.T) {
	if _, err := seal.New(strings.Repeat("a", seal.MinSecretLength-1)); err == nil {
		t.Error("accepted a short secret")
	}
}
