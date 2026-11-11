// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package twofactor is TOTP (RFC 6238) and recovery codes. It works out which 30-second
// step a code was for, so that the caller can refuse one it has seen before.
package twofactor

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	issuer = "andante"
	period = 30
	// skew is how many steps either side of now a code is taken for, for a phone
	// whose clock is a little out.
	skew = 1

	recoveryCount = 10
	recoveryBytes = 10
	qrSize        = 256
)

var opts = totp.ValidateOpts{Period: period, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// NewSecret returns a new random secret, base32 as authenticator apps take it.
func NewSecret(account string) (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account, Period: period})
	if err != nil {
		return "", fmt.Errorf("generate a TOTP secret: %w", err)
	}
	return key.Secret(), nil
}

func keyOf(secret, account string) (*otp.Key, error) {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return nil, fmt.Errorf("decode the TOTP secret: %w", err)
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account, Period: period, Secret: raw})
	if err != nil {
		return nil, fmt.Errorf("make a TOTP key: %w", err)
	}
	return key, nil
}

// URL is the otpauth:// address an authenticator app takes, the same as the QR code holds.
func URL(secret, account string) (string, error) {
	key, err := keyOf(secret, account)
	if err != nil {
		return "", err
	}
	return key.URL(), nil
}

// QR returns a PNG of the QR code for an authenticator app to scan.
func QR(secret, account string) ([]byte, error) {
	key, err := keyOf(secret, account)
	if err != nil {
		return nil, err
	}
	img, err := key.Image(qrSize, qrSize)
	if err != nil {
		return nil, fmt.Errorf("draw the QR code: %w", err)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("encode the QR code: %w", err)
	}
	return out.Bytes(), nil
}

// Check reports whether code is right for now, give or take a step, and which step it
// was for.
func Check(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != int(otp.DigitsSix) {
		return 0, false
	}
	for i := -skew; i <= skew; i++ {
		at := now.Add(time.Duration(i) * period * time.Second)
		want, err := totp.GenerateCodeCustom(secret, at, opts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return at.Unix() / period, true
		}
	}
	return 0, false
}

// IsCode reports whether given looks like a TOTP code and not a recovery code.
func IsCode(given string) bool {
	given = strings.TrimSpace(given)
	if len(given) != int(otp.DigitsSix) {
		return false
	}
	for _, c := range given {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// RecoveryCodes returns new single-use codes, each 80 random bits, written in groups
// of four so that they can be copied out by hand.
func RecoveryCodes() ([]string, error) {
	codes := make([]string, recoveryCount)
	for i := range codes {
		b := make([]byte, recoveryBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("read random bytes: %w", err)
		}
		s := strings.ToLower(recoveryEncoding.EncodeToString(b))
		var groups []string
		for len(s) > 0 {
			n := min(4, len(s))
			groups = append(groups, s[:n])
			s = s[n:]
		}
		codes[i] = strings.Join(groups, "-")
	}
	return codes, nil
}

// HashRecovery is what's stored for a recovery code. It ignores case, spaces and dashes.
func HashRecovery(code string) []byte {
	code = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}
