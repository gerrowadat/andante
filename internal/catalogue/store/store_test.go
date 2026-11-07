// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/jackc/pgx/v5/pgconn"
)

func source(t *testing.T, s *store.Store, slug string) db.Source {
	t.Helper()
	created, err := s.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: slug, Title: "t", HomeUrl: "https://example.com/", DefaultKind: "page",
		Strategy: "crawl", Config: []byte(`{"start_url":"https://example.com/` + slug + `"}`), State: "indexing",
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func index(t *testing.T, s *store.Store, sourceID int64, urls ...string) {
	t.Helper()
	entries := make([]store.NewEntry, 0, len(urls))
	for i := range urls {
		entries = append(entries, store.NewEntry{Kind: "page", URL: &urls[i], Images: []string{}, Enclosures: []byte("[]"), Extra: []byte("{}"), ContentHash: []byte(urls[i])})
	}
	err := s.SaveBatch(t.Context(), entries, nil, store.Outcome{SourceID: sourceID, Cursor: []byte("{}"), State: "indexing"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRevisionsCoverEveryChange(t *testing.T) {
	s := storetest.Catalogue(t)
	ctx := t.Context()
	a := source(t, s, "a")
	index(t, s, a.ID, "https://example.com/1", "https://example.com/2")

	// SaveBatch touched the source after its entries, so it comes last, once.
	changes, err := s.ChangesAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].Entry == nil || changes[1].Entry == nil || changes[2].Source == nil {
		t.Fatalf("changes = %+v", changes)
	}
	for i := 1; i < len(changes); i++ {
		if changes[i].Revision <= changes[i-1].Revision {
			t.Errorf("revisions out of order: %+v", changes)
		}
	}

	last := changes[2].Revision
	limited, err := s.ChangesAfter(ctx, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].Revision != changes[0].Revision {
		t.Errorf("limit: %+v", limited)
	}
	later, err := s.ChangesAfter(ctx, last, 100)
	if err != nil || len(later) != 0 {
		t.Errorf("after the last revision: %+v, %v", later, err)
	}
}

func TestDemandIsNotAChange(t *testing.T) {
	s := storetest.Catalogue(t)
	ctx := t.Context()
	a := source(t, s, "a")
	changes, err := s.ChangesAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	last := changes[len(changes)-1].Revision

	seq := int32(4)
	if err := s.SetDemand(ctx, a.ID, &seq); err != nil {
		t.Fatal(err)
	}
	later, err := s.ChangesAfter(ctx, last, 100)
	if err != nil || len(later) != 0 {
		t.Errorf("demand made a change: %+v, %v", later, err)
	}
	got, has, err := s.Furthest(ctx, a.ID)
	if err != nil || !has || got != 4 {
		t.Errorf("Furthest = %d, %v, %v", got, has, err)
	}
}

func TestDeletingASource(t *testing.T) {
	s := storetest.Catalogue(t)
	ctx := t.Context()
	a := source(t, s, "a")
	index(t, s, a.ID, "https://example.com/1")

	seq := int32(0)
	if err := s.SetDemand(ctx, a.ID, &seq); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource(ctx, a.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete with demand = %v", err)
	}
	if err := s.SetDemand(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if err := s.SetDemand(ctx, a.ID, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("demand on a deleted source = %v", err)
	}

	changes, err := s.ChangesAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].DeletedSource == nil || *changes[0].DeletedSource != a.ID {
		t.Errorf("changes after a delete = %+v", changes)
	}
}

// A reader must never pass a revision that hasn't committed yet, or it would miss it.
// Writing from many goroutines at once, whatever anyone has seen has no gaps behind it.
func TestRevisionsCommitInOrder(t *testing.T) {
	s := storetest.Catalogue(t)
	ctx := t.Context()
	var sources [8]db.Source
	for i := range sources {
		sources[i] = source(t, s, string(rune('a'+i)))
	}

	var writers sync.WaitGroup
	for _, src := range sources {
		writers.Go(func() {
			for i := range 20 {
				_, err := s.UpdateSource(ctx, src.ID, func(p *db.UpdateSourceParams) error {
					p.Title = string(rune('a' + i))
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	done := make(chan struct{})
	go func() {
		writers.Wait()
		close(done)
	}()

	var after int64
	seen := map[int64]int64{}
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true
		default:
		}
		changes, err := s.ChangesAfter(ctx, after, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range changes {
			if c.Revision <= after {
				t.Fatalf("revision %d came after %d", c.Revision, after)
			}
			after = c.Revision
			seen[c.Source.ID] = c.Source.Revision
		}
	}
	final, err := s.ChangesAfter(ctx, after, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range final {
		seen[c.Source.ID] = c.Source.Revision
	}
	for _, src := range sources {
		current, err := s.GetSource(ctx, src.ID)
		if err != nil {
			t.Fatal(err)
		}
		if seen[src.ID] != current.Revision {
			t.Errorf("source %d: reader saw revision %d, it's at %d", src.ID, seen[src.ID], current.Revision)
		}
	}
}

func TestEntryIdentity(t *testing.T) {
	const insert = "INSERT INTO entries (source_id, seq, kind, url, guid) VALUES ($1, $2, 'page', $3, $4)"
	s := storetest.Catalogue(t)
	ctx := t.Context()
	var source int64
	err := s.Pool().QueryRow(ctx, `INSERT INTO sources (slug, title, home_url, default_kind, strategy, config, state, canonical_key, home_key)
		VALUES ('s', 't', 'https://example.com/', 'page', 'crawl', '{}', 'indexing', 'k', 'h') RETURNING id`).Scan(&source)
	if err != nil {
		t.Fatal(err)
	}

	url, guidA, guidB := "https://example.com/show", "a", "b"
	steps := []struct {
		name      string
		seq       int
		url, guid *string
		wantDup   bool
	}{
		{name: "first with a guid", seq: 1, url: &url, guid: &guidA},
		{name: "same link, different guid", seq: 2, url: &url, guid: &guidB},
		{name: "same guid", seq: 3, guid: &guidA, wantDup: true},
		{name: "no link, new guid", seq: 4, guid: new("c")},
		{name: "first without a guid", seq: 5, url: new("https://example.com/1")},
		{name: "same URL without a guid", seq: 6, url: new("https://example.com/1"), wantDup: true},
		{name: "link shared with a guid entry", seq: 7, url: &url},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			_, err := s.Pool().Exec(ctx, insert, source, step.seq, step.url, step.guid)
			var pgErr *pgconn.PgError
			gotDup := errors.As(err, &pgErr) && pgErr.Code == "23505"
			if gotDup != step.wantDup || (err != nil && !gotDup) {
				t.Errorf("insert: %v, want duplicate %v", err, step.wantDup)
			}
		})
	}
}

func TestDemandWakesAParkedSource(t *testing.T) {
	s := storetest.Catalogue(t)
	ctx := t.Context()
	a := source(t, s, "a")
	if err := s.RecordOutcome(ctx, store.Outcome{SourceID: a.ID, Cursor: []byte("{}"), State: "tail"}); err != nil {
		t.Fatal(err)
	}
	seq := int32(0)
	if err := s.SetDemand(ctx, a.ID, &seq); err != nil {
		t.Fatal(err)
	}
	woken, err := s.GetSource(ctx, a.ID)
	if err != nil || woken.DueAt == nil {
		t.Fatalf("due_at = %v, %v", woken.DueAt, err)
	}
	changes, err := s.ChangesAfter(ctx, 0, 100)
	if err != nil || changes[len(changes)-1].Revision != woken.Revision {
		t.Errorf("waking wasn't a change the server would see: %v", err)
	}
}

// A requested crawl, a delete or waking a source used to lock the row and then take
// the revision lock, the other way round to SaveBatch. When they met, Postgres killed
// one of them. Postgres waits a second before it looks for deadlocks, so this needs a
// few goes to be sure.
func TestWritesDontDeadlockWithACrawl(t *testing.T) {
	s := storetest.Catalogue(t)
	ctx := t.Context()
	a := source(t, s, "a")
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range 40 {
		url := fmt.Sprintf("https://example.com/%d", i)
		wg.Go(func() {
			entry := store.NewEntry{Kind: "page", URL: &url, Images: []string{}, Enclosures: []byte("[]"), Extra: []byte("{}"), ContentHash: []byte(url)}
			// A batch that touches the source again after its entries, as a crawl does.
			err := s.SaveBatch(ctx, []store.NewEntry{entry}, nil, store.Outcome{SourceID: a.ID, Cursor: []byte("{}"), State: "tail"})
			if err != nil && !errors.Is(err, store.ErrConflict) {
				errs <- err
			}
		})
		wg.Go(func() {
			if err := s.RequestCrawl(ctx, a.ID); err != nil {
				errs <- err
			}
		})
		wg.Go(func() {
			seq := int32(i)
			if err := s.SetDemand(ctx, a.ID, &seq); err != nil {
				errs <- err
			}
		})
		wg.Go(func() {
			if err := s.RecordOutcome(ctx, store.Outcome{SourceID: a.ID, Cursor: []byte("{}"), State: "tail"}); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
