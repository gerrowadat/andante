// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/seal"
	"github.com/gerrowadat/andante/internal/store/storetest"
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
	code = run(context.Background(), args, &out, &errOut, func(c int) { panic(exited(c)) })
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

func TestMissingRequiredFlagsFail(t *testing.T) {
	code, _, stderr := runCaptured(t)
	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	for _, want := range []string{"--database-url is required", "--base-url is required"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not contain %q", stderr, want)
		}
	}
}

func TestUnreachableDatabaseFails(t *testing.T) {
	code, _, stderr := runCaptured(t,
		"--database-url=postgres://andante@127.0.0.1:1/andante?connect_timeout=1",
		"--base-url=https://andante.example",
		"--catalogue-url=http://127.0.0.1:1/v1",
		"--catalogue-token=token",
		"--secret="+strings.Repeat("s", 32),
		"--log-format=text",
	)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "level=ERROR") {
		t.Errorf("stderr %q has no error log", stderr)
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	newLogger("json", "warn", &buf).Info("hidden")
	if buf.Len() != 0 {
		t.Errorf("info logged at warn level: %s", buf.String())
	}
	newLogger("json", "warn", &buf).Warn("shown")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Errorf("json format is not JSON: %v", err)
	}

	buf.Reset()
	newLogger("text", "debug", &buf).Debug("shown")
	if !strings.Contains(buf.String(), "msg=shown") {
		t.Errorf("text format output %q", buf.String())
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

func TestUserCommands(t *testing.T) {
	dsn := storetest.DSN(t)
	code, token, stderr := runCaptured(t, "user", "add", "dave", "--admin", "--database-url="+dsn)
	if code != 0 || len(strings.TrimSpace(token)) != 43 {
		t.Fatalf("user add: exit %d, %q, %q", code, token, stderr)
	}
	st := storetest.Open(t, dsn)
	actor, ok, err := st.AuthenticateAPIToken(t.Context(), strings.TrimSpace(token))
	if err != nil || !ok || !actor.Admin {
		t.Errorf("the token: %+v, %v, %v", actor, ok, err)
	}
	if code, _, _ := runCaptured(t, "user", "add", "dave", "--database-url="+dsn); code != 1 {
		t.Errorf("a second dave: exit %d", code)
	}

	code, fresh, _ := runCaptured(t, "user", "reset", "dave", "--database-url="+dsn)
	if code != 0 {
		t.Fatalf("user reset: exit %d", code)
	}
	if _, ok, _ := st.AuthenticateAPIToken(t.Context(), strings.TrimSpace(fresh)); !ok {
		t.Error("the new token doesn't work")
	}
	if _, ok, _ := st.AuthenticateAPIToken(t.Context(), strings.TrimSpace(token)); ok {
		t.Error("the old token survived a reset")
	}
}

func TestRotateSecretCommand(t *testing.T) {
	dsn := storetest.DSN(t)
	st := storetest.Open(t, dsn)
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, second := strings.Repeat("a", 32), strings.Repeat("b", 32)
	box, err := seal.New(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UseBox(t.Context(), box); err != nil {
		t.Fatal(err)
	}

	if code, _, _ := runCaptured(t, "rotate-secret", "--database-url="+dsn, "--secret="+second, "--new-secret="+first); code != 1 {
		t.Errorf("rotating from the wrong secret: exit %d", code)
	}
	if code, _, stderr := runCaptured(t, "rotate-secret", "--database-url="+dsn, "--secret="+first, "--new-secret="+second); code != 0 {
		t.Errorf("rotating: exit %d, %q", code, stderr)
	}
	if code, _, stderr := runCaptured(t, "rotate-secret", "--database-url="+dsn, "--forget-urls", "--new-secret="+first); code != 0 {
		t.Errorf("forgetting: exit %d, %q", code, stderr)
	}
}
