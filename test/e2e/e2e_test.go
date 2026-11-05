// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

//go:build e2e

// Package e2e runs the real binaries as subprocesses. It is behind a build tag
// because it belongs to the release suite, not to fast CI.
package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const version = "v0.0.0-e2e"

func TestServerReportsVersion(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "andante-server")
	ldflags := "-X github.com/gerrowadat/andante/internal/buildinfo.version=" + version
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "../../cmd/andante-server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("andante-server --version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), version) {
		t.Errorf("output %q does not contain %q", out, version)
	}
}
