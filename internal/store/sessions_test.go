// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"maps"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func TestCountSessions(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	if got, err := s.CountSessions(ctx, now); err != nil || len(got) != 0 {
		t.Errorf("none = %v, %v", got, err)
	}
	user, _, err := s.CreateUser(ctx, "dave", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	session := func(at time.Time) string {
		raw, err := s.CreateSession(ctx, user.ID, client, at)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	session(now)
	session(now.Add(-time.Minute))
	session(now.Add(-store.SessionAbsolute - time.Minute))
	session(now.Add(-store.SessionIdle - time.Minute))
	if err := s.EndSession(ctx, session(now), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "UPDATE sessions SET method = $1 WHERE id = (SELECT max(id) FROM sessions WHERE revoked_at IS NULL AND created_at > $2)",
		store.MethodOIDC, now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{store.MethodPassword: 1, store.MethodOIDC: 1}
	if got, err := s.CountSessions(ctx, now); err != nil || !maps.Equal(got, want) {
		t.Errorf("live = %v, %v, want %v", got, err, want)
	}
}
