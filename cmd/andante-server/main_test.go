// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/buildinfo"
)

// exited carries the code kong passes to its exit function, which must stop
// the parse the way os.Exit would.
type exited int

func runCaptured(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(exited)
			if !ok {
				panic(r)
			}
			code, stdout, stderr = int(e), out.String(), errOut.String()
		}
	}()
	code = run(args, &out, &errOut, func(c int) { panic(exited(c)) })
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		t.Run(args[0], func(t *testing.T) {
			code, stdout, _ := runCaptured(t, args...)
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if got, want := strings.TrimSpace(stdout), buildinfo.String(); got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestEmbedsTimeZoneData(t *testing.T) {
	// A LoadLocation test would pass on any machine with zoneinfo installed,
	// which is the one case the embedded data is not for.
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "time/tzdata" {
			return
		}
	}
	t.Error("main.go does not import time/tzdata")
}

func TestUnknownArgumentFails(t *testing.T) {
	code, _, stderr := runCaptured(t, "--no-such-flag")
	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	if stderr == "" {
		t.Error("stderr is empty, want an error message")
	}
}
