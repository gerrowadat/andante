// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package replica_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func addSource(t *testing.T, s *stacktest.Stack, slug string) client.Source {
	t.Helper()
	var config client.SourceConfig
	if err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: "https://" + slug + ".example/rss"}); err != nil {
		t.Fatal(err)
	}
	created, err := s.Client.CreateSource(t.Context(), replica.SourceCreate{SourceCreate: client.SourceCreate{
		Slug: slug, Title: "t", HomeURL: "https://a.example/", DefaultKind: client.Page, Config: config,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func index(t *testing.T, s *stacktest.Stack, sourceID int64, url string) {
	t.Helper()
	title := "Title of " + url
	err := s.Catalogue.SaveBatch(t.Context(), []catalogue.NewEntry{{
		Kind: "page", URL: &url, Title: &title, Images: []string{url + ".png"},
		Enclosures: []byte("[]"), Extra: []byte("{}"), Duration: 90 * time.Second, ContentHash: []byte(url),
	}}, nil, catalogue.Outcome{SourceID: sourceID, Cursor: []byte("{}"), State: "indexing"})
	if err != nil {
		t.Fatal(err)
	}
}

func catchUp(t *testing.T, s *stacktest.Stack) {
	t.Helper()
	if err := s.Sync.CatchUp(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCopyFollowsTheCatalogue(t *testing.T) {
	s := stacktest.New(t)
	ctx := t.Context()
	source := addSource(t, s, "a")
	index(t, s, source.ID, "https://a.example/1")
	catchUp(t, s)

	got, err := s.Store.GetSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "a" || got.Strategy != "feed" || got.State != "indexing" {
		t.Errorf("source = %+v", got)
	}
	entries, err := s.Store.ListEntries(ctx, source.ID, -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	e := entries[0]
	if *e.Title != "Title of https://a.example/1" || e.Images[0] != "https://a.example/1.png" {
		t.Errorf("entry = %+v", e)
	}
	if !e.Duration.Valid || e.Duration.Microseconds != int64(90*time.Second/time.Microsecond) {
		t.Errorf("duration = %+v", e.Duration)
	}
	if e.AvailableAt.Before(e.DiscoveredAt) {
		t.Errorf("available at %s, before it was discovered at %s", e.AvailableAt, e.DiscoveredAt)
	}

	// A change to a known entry reaches the copy, and doesn't reset when it arrived.
	title, url := "Better", "https://a.example/1"
	err = s.Catalogue.SaveBatch(ctx, nil, []catalogue.EntryUpdate{{URL: url, Title: &title, Images: []string{}}},
		catalogue.Outcome{SourceID: source.ID, Cursor: []byte("{}"), State: "tail"})
	if err != nil {
		t.Fatal(err)
	}
	catchUp(t, s)
	updated, err := s.Store.ListEntries(ctx, source.ID, -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if *updated[0].Title != "Better" {
		t.Errorf("title = %q", *updated[0].Title)
	}
	if !updated[0].AvailableAt.Equal(e.AvailableAt) {
		t.Errorf("available_at moved from %s to %s", e.AvailableAt, updated[0].AvailableAt)
	}
	if again, err := s.Store.GetSource(ctx, source.ID); err != nil || again.State != "tail" {
		t.Errorf("state = %q, %v", again.State, err)
	}
}

func TestAnEntryCanArriveBeforeItsSource(t *testing.T) {
	s := stacktest.New(t)
	ctx := t.Context()
	url := "https://a.example/1"
	err := s.Store.ApplyChanges(ctx, []store.Change{{Revision: 1, Entry: &db.UpsertEntryParams{
		ID: 1, SourceID: 5, Seq: 0, Kind: "page", Url: &url, Images: []string{}, Enclosures: []byte("[]"), Extra: []byte("{}"),
		DiscoveredAt: time.Now(),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.ListEntries(ctx, 5, -1, 10); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("entries of a source not here yet: %v", err)
	}
	err = s.Store.ApplyChanges(ctx, []store.Change{{Revision: 2, Source: &db.UpsertSourceParams{
		ID: 5, Slug: "a", Title: "t", HomeUrl: url, DefaultKind: "page", Extra: []byte("{}"), Strategy: "feed",
		Config: []byte("{}"), State: "tail", CreatedAt: time.Now(),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.Store.ListEntries(ctx, 5, -1, 10)
	if err != nil || len(entries) != 1 {
		t.Errorf("entries = %d, %v", len(entries), err)
	}
}

func TestAChangeAppliedTwiceDoesNothing(t *testing.T) {
	s := stacktest.New(t)
	ctx := t.Context()
	params := func(title string) *db.UpsertSourceParams {
		return &db.UpsertSourceParams{
			ID: 5, Slug: "a", Title: title, HomeUrl: "https://a.example/", DefaultKind: "page", Extra: []byte("{}"),
			Strategy: "feed", Config: []byte("{}"), State: "tail", CreatedAt: time.Now(),
		}
	}
	if err := s.Store.ApplyChanges(ctx, []store.Change{{Revision: 10, Source: params("new")}}); err != nil {
		t.Fatal(err)
	}
	// A slower sync turning up with something older.
	if err := s.Store.ApplyChanges(ctx, []store.Change{{Revision: 9, Source: params("old")}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Store.GetSource(ctx, 5)
	if err != nil || got.Title != "new" {
		t.Errorf("title = %q, %v", got.Title, err)
	}
	if revision, err := s.Store.SyncRevision(ctx); err != nil || revision != 10 {
		t.Errorf("revision = %d, %v", revision, err)
	}
}

func TestDeletedSources(t *testing.T) {
	s := stacktest.New(t)
	ctx := t.Context()
	source := addSource(t, s, "a")
	index(t, s, source.ID, "https://a.example/1")
	catchUp(t, s)
	if err := s.Client.DeleteSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	catchUp(t, s)
	if _, err := s.Store.GetSource(ctx, source.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("source after delete: %v", err)
	}
	var entries int
	if err := s.Store.Pool().QueryRow(ctx, "SELECT count(*) FROM entries").Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Errorf("entries left behind: %d", entries)
	}
}

func TestADeletedSourceWithAReadStays(t *testing.T) {
	s := stacktest.New(t)
	ctx := t.Context()
	source := addSource(t, s, "a")
	index(t, s, source.ID, "https://a.example/1")
	catchUp(t, s)
	actor, _ := storetest.User(t, s.Store, "reader", false)
	if _, err := s.Reads.Create(ctx, reads.CreateParams{
		UserID: actor.UserID, SourceID: source.ID, Name: "r", Initial: 1, Pacing: schedule.RRulePacing{RRule: "FREQ=DAILY", TimeZone: "UTC", PerRelease: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// The catalogue hasn't heard about the read yet, so it allows this.
	if err := s.Client.DeleteSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	catchUp(t, s)
	if _, err := s.Store.GetSource(ctx, source.ID); err != nil {
		t.Errorf("the source went from under a read: %v", err)
	}
	if entries, err := s.Store.ListEntries(ctx, source.ID, -1, 10); err != nil || len(entries) != 1 {
		t.Errorf("entries = %d, %v", len(entries), err)
	}
}

func TestDemandFollowsReads(t *testing.T) {
	s := stacktest.New(t)
	ctx := t.Context()
	source := addSource(t, s, "a")
	index(t, s, source.ID, "https://a.example/1")
	catchUp(t, s)

	if err := s.Sync.PushDemand(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if demand, err := s.Catalogue.Demand(ctx, source.ID); err != nil || demand != nil {
		t.Errorf("demand with no reads = %v, %v", demand, err)
	}

	actor, _ := storetest.User(t, s.Store, "reader", false)
	read, err := s.Reads.Create(ctx, reads.CreateParams{
		UserID: actor.UserID, SourceID: source.ID, Name: "r", Initial: 1, Pacing: schedule.RRulePacing{RRule: "FREQ=DAILY", TimeZone: "UTC", PerRelease: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sync.PushDemand(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if demand, err := s.Catalogue.Demand(ctx, source.ID); err != nil || demand == nil || *demand != 0 {
		t.Errorf("demand with a read = %v, %v", demand, err)
	}

	if err := s.Reads.Delete(ctx, actor, read.Read.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync.PushDemand(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if demand, err := s.Catalogue.Demand(ctx, source.ID); err != nil || demand != nil {
		t.Errorf("demand after the last read went = %v, %v", demand, err)
	}
}

func TestACatalogueOutageIsAProblem(t *testing.T) {
	s := stacktest.New(t)
	s.CatalogueServer.Close()
	err := s.Sync.CatchUp(t.Context())
	var p *httpapi.Error
	if !errors.As(err, &p) || p.Problem.Status != http.StatusServiceUnavailable || p.Problem.Type != httpapi.TypeCatalogueDown {
		t.Errorf("CatchUp = %v", err)
	}
}

func TestProblemsPassThrough(t *testing.T) {
	s := stacktest.New(t)
	err := s.Client.DeleteSource(t.Context(), 999)
	var p *httpapi.Error
	if !errors.As(err, &p) || p.Problem.Status != http.StatusNotFound {
		t.Errorf("DeleteSource = %v", err)
	}
}

func TestRunKeepsTheCopyCurrent(t *testing.T) {
	s := stacktest.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		s.Sync.Run(ctx)
		close(stopped)
	}()

	source := addSource(t, s, "a")
	index(t, s, source.ID, "https://a.example/1")
	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, err := s.Store.ListEntries(t.Context(), source.ID, -1, 10)
		if err == nil && len(entries) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the entry never arrived: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if got := testutil.CollectAndCount(s.Registry, "andante_sync_changes_total"); got != 1 {
		t.Errorf("metric series = %d", got)
	}
}
