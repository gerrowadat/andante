// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/prometheus/client_golang/prometheus"
)

const ip = "203.0.113.9"

var flagToken = strings.Repeat("t", 32)

type fixture struct {
	st   *store.Store
	svc  *bootstrap.Service
	reg  *prometheus.Registry
	logs *bytes.Buffer
}

func newFixture(t *testing.T, token string) fixture {
	t.Helper()
	f := fixture{st: storetest.New(t), reg: prometheus.NewRegistry(), logs: &bytes.Buffer{}}
	f.svc = bootstrap.New(bootstrap.Options{
		Store: f.st, Token: token, BaseURL: "https://feeds.example/", Registry: f.reg,
		Logger: slog.New(slog.NewJSONHandler(f.logs, nil)),
	})
	return f
}

// logged returns the last log line.
func (f fixture) logged(t *testing.T) map[string]string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
	var line map[string]string
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &line); err != nil {
		t.Fatalf("log %q: %v", f.logs.String(), err)
	}
	return line
}

func count(t *testing.T, reg *prometheus.Registry, result string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "andante_bootstrap_attempts_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			if m.GetLabel()[0].GetValue() == result {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestAGeneratedTokenIsLoggedAndWorks(t *testing.T) {
	f := newFixture(t, "")
	ctx := t.Context()
	if err := f.svc.Announce(ctx); err != nil {
		t.Fatal(err)
	}
	line := f.logged(t)
	if line["level"] != "WARN" || line["url"] != "https://feeds.example/setup" || line["token"] == "" || line["until"] == "" {
		t.Fatalf("logged %v", line)
	}
	if open, err := f.svc.Open(ctx); err != nil || !open {
		t.Errorf("open = %v, %v", open, err)
	}
	if _, err := f.svc.Invite(ctx, "wrong", "dave", ip); !errors.Is(err, bootstrap.ErrWrongToken) {
		t.Errorf("a wrong token = %v", err)
	}
	raw, err := f.svc.Invite(ctx, line["token"], "dave", ip)
	if err != nil || raw == "" {
		t.Fatalf("invite = %q, %v", raw, err)
	}
	invite, err := f.st.LookupInvite(ctx, raw, time.Now())
	if err != nil || invite.Name != "dave" || invite.Role != store.RoleAdmin {
		t.Errorf("invite = %+v, %v", invite, err)
	}
	if count(t, f.reg, "ok") != 1 || count(t, f.reg, "wrong") != 1 {
		t.Errorf("ok, wrong = %v, %v", count(t, f.reg, "ok"), count(t, f.reg, "wrong"))
	}
	lines, err := f.st.ListAudit(ctx, 0, 10)
	if err != nil || len(lines) != 2 || lines[0].Action != "bootstrap.failed" || lines[1].Action != "bootstrap" || lines[1].Target != "dave" || lines[1].Ip != ip {
		t.Errorf("audit = %+v, %v", lines, err)
	}
}

func TestTheTokenFromTheFlag(t *testing.T) {
	f := newFixture(t, flagToken)
	ctx := t.Context()
	if err := f.svc.Announce(ctx); err != nil {
		t.Fatal(err)
	}
	if line := f.logged(t); line["level"] != "WARN" || line["token"] != "" || line["url"] == "" {
		t.Errorf("logged %v", line)
	}
	admin, err := f.svc.Make(ctx, flagToken, "dave", ip)
	if err != nil || admin.User.Role != store.RoleAdmin || admin.APIToken == "" || admin.Invite == "" {
		t.Fatalf("make = %+v, %v", admin, err)
	}
	if _, ok, err := f.st.AuthenticateAPIToken(ctx, admin.APIToken); !ok || err != nil {
		t.Errorf("the API token doesn't work: %v", err)
	}
	if _, err := f.st.LookupInvite(ctx, admin.Invite, time.Now()); err != nil {
		t.Errorf("the password link doesn't work: %v", err)
	}

	f.logs.Reset()
	if err := f.svc.Announce(ctx); err != nil {
		t.Fatal(err)
	}
	if line := f.logged(t); line["level"] != "INFO" {
		t.Errorf("with an admin, logged %v", line)
	}
}

func TestAnAdminClosesSetup(t *testing.T) {
	f := newFixture(t, flagToken)
	ctx := t.Context()
	if _, _, err := f.st.CreateUser(ctx, "dave", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if open, err := f.svc.Open(ctx); err != nil || open {
		t.Errorf("open = %v, %v", open, err)
	}
	if _, err := f.svc.Invite(ctx, flagToken, "sam", ip); !errors.Is(err, bootstrap.ErrClosed) {
		t.Errorf("invite = %v", err)
	}
	if _, err := f.svc.Make(ctx, flagToken, "sam", ip); !errors.Is(err, bootstrap.ErrClosed) {
		t.Errorf("make = %v", err)
	}
	if count(t, f.reg, "closed") != 2 {
		t.Errorf("closed = %v", count(t, f.reg, "closed"))
	}

	// With an admin, no token is made or logged.
	g := newFixture(t, "")
	if _, _, err := g.st.CreateUser(ctx, "dave", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if open, err := g.svc.Open(ctx); err != nil || open {
		t.Errorf("open with no token = %v, %v", open, err)
	}
	if err := g.svc.Announce(ctx); err != nil {
		t.Fatal(err)
	}
	if g.logs.Len() != 0 {
		t.Errorf("logged with an admin: %s", g.logs)
	}
}

func TestTheTokenNeverTakesOverAnAccount(t *testing.T) {
	f := newFixture(t, flagToken)
	ctx := t.Context()
	if _, _, err := f.st.CreateUser(ctx, "dave", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Invite(ctx, flagToken, "dave", ip); !errors.Is(err, store.ErrConflict) {
		t.Errorf("invite = %v", err)
	}
	if _, err := f.svc.Make(ctx, flagToken, "dave", ip); !errors.Is(err, store.ErrConflict) {
		t.Errorf("make = %v", err)
	}
	if count(t, f.reg, "refused") != 2 {
		t.Errorf("refused = %v", count(t, f.reg, "refused"))
	}
}

func TestGuessingIsThrottled(t *testing.T) {
	f := newFixture(t, flagToken)
	ctx := t.Context()
	var err error
	for range 30 {
		if _, err = f.svc.Invite(ctx, "guess", "dave", ip); err != nil && !errors.Is(err, bootstrap.ErrWrongToken) {
			break
		}
	}
	var throttled *bootstrap.ThrottledError
	if !errors.As(err, &throttled) || throttled.Wait <= 0 {
		t.Fatalf("after 30 guesses = %v", err)
	}
	if _, err := f.svc.Invite(ctx, flagToken, "dave", ip); !errors.As(err, &throttled) {
		t.Errorf("the right token while throttled = %v", err)
	}
	if _, err := f.svc.Invite(ctx, flagToken, "dave", "198.51.100.1"); err != nil {
		t.Errorf("from another address = %v", err)
	}
	if count(t, f.reg, "throttled") == 0 {
		t.Error("nothing counted as throttled")
	}
}
