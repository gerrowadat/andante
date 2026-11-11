// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package twofactor_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/twofactor"
	"github.com/pquerna/otp/totp"
)

func TestCheck(t *testing.T) {
	secret, err := twofactor.NewSecret("dave")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 7, 0, 10, 0, time.UTC)
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := twofactor.Check(secret, code, now)
	if !ok || step != now.Unix()/30 {
		t.Errorf("now: %d %v", step, ok)
	}
	if step, ok := twofactor.Check(secret, " "+code+" ", now.Add(30*time.Second)); !ok || step != now.Unix()/30 {
		t.Errorf("a step later: %d %v", step, ok)
	}
	if _, ok := twofactor.Check(secret, code, now.Add(2*time.Minute)); ok {
		t.Error("a code two minutes old worked")
	}
	if _, ok := twofactor.Check(secret, "12345", now); ok {
		t.Error("five digits worked")
	}
	if _, ok := twofactor.Check("not base32!", code, now); ok {
		t.Error("a broken secret worked")
	}
}

func TestQRAndURL(t *testing.T) {
	secret, err := twofactor.NewSecret("dave")
	if err != nil {
		t.Fatal(err)
	}
	u, err := twofactor.URL(secret, "dave")
	if err != nil || !strings.HasPrefix(u, "otpauth://totp/andante:dave?") || !strings.Contains(u, "secret="+secret) {
		t.Errorf("URL = %q, %v", u, err)
	}
	img, err := twofactor.QR(secret, "dave")
	if err != nil || !bytes.HasPrefix(img, []byte("\x89PNG")) {
		t.Errorf("QR: %v", err)
	}
	if _, err := twofactor.QR("!!", "dave"); err == nil {
		t.Error("a broken secret made a QR code")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := twofactor.RecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 19 || seen[c] || twofactor.IsCode(c) {
			t.Errorf("code %q", c)
		}
		seen[c] = true
	}
	if len(codes) != 10 {
		t.Errorf("%d codes", len(codes))
	}
	a := twofactor.HashRecovery(codes[0])
	b := twofactor.HashRecovery(" " + strings.ToUpper(strings.ReplaceAll(codes[0], "-", " ")))
	if !bytes.Equal(a, b) {
		t.Error("the hash depends on case, spaces or dashes")
	}
	if !twofactor.IsCode("123456") || twofactor.IsCode("12345a") || twofactor.IsCode("1234567") {
		t.Error("IsCode")
	}
}
