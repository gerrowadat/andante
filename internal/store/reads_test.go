// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func TestDeletingTheReadsOfASource(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	source, err := storetest.AddSource(t, s, db.UpsertSourceParams{
		Slug: "s", Title: "t", HomeUrl: "https://a.example/", DefaultKind: "page", Strategy: "feed", Config: []byte("{}"), State: "tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := storetest.User(t, s, "alice", false)
	bob, _ := storetest.User(t, s, "bob", false)
	admin, _ := storetest.User(t, s, "dave", true)
	for _, actor := range []store.Actor{alice, bob} {
		_, err := s.CreateRead(ctx, db.InsertReadParams{
			UserID: actor.UserID, SourceID: source.ID, Name: "r", CatchUp: "passthrough", Config: []byte("{}"),
		}, db.InsertSegmentParams{FromSeq: 0, StartsAt: time.Now(), Pacing: "paused", Timezone: "UTC", PerRelease: 1, Reason: "created", CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}

	if deleted, err := s.DeleteReadsOfSource(ctx, alice, source.ID); err != nil || deleted != 1 {
		t.Errorf("alice deleting = %d, %v; want only her own", deleted, err)
	}
	if deleted, err := s.DeleteReadsOfSource(ctx, admin, source.ID); err != nil || deleted != 1 {
		t.Errorf("an admin deleting = %d, %v; want bob's", deleted, err)
	}
	if count, err := s.CountReads(ctx, source.ID); err != nil || count != 0 {
		t.Errorf("reads left = %d, %v", count, err)
	}
}
