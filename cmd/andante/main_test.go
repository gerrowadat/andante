// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/buildinfo"
)

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		t.Run(args[0], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := newRootCmd(&stdout, &stderr)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() = %v, stderr %q", err, stderr.String())
			}
			if got, want := strings.TrimSpace(stdout.String()), buildinfo.String(); got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := newRootCmd(&stdout, &stderr)
	root.SetArgs([]string{"no-such-command"})
	if err := root.Execute(); err == nil {
		t.Error("Execute() = nil, want an error")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}
