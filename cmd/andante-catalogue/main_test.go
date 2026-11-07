// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func capture(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr, func(int) {})
	return code, stdout.String(), stderr.String()
}

func TestVersion(t *testing.T) {
	if code, stdout, _ := capture("version"); code != 0 || stdout == "" {
		t.Errorf("version: exit %d, stdout %q", code, stdout)
	}
}

func TestSettingsAreRequired(t *testing.T) {
	code, _, stderr := capture("serve")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	for _, want := range []string{"--database-url is required", "--api-token is required", "--base-url is required"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not contain %q", stderr, want)
		}
	}
}

func TestUnreachableDatabaseFails(t *testing.T) {
	code, _, stderr := capture(
		"--database-url=postgres://andante@127.0.0.1:1/andante?connect_timeout=1",
		"--base-url=https://andante.example", "--api-token=token", "--log-format=text",
	)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "level=ERROR") {
		t.Errorf("stderr %q has no error log", stderr)
	}
}
