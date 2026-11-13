// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package app is what the API and the website both do, with the side effects that go
// with it: keeping the copy of the catalogue current after a change, telling the
// catalogue how far readers have got, and starting a new source when someone reads it.
// Each caller checks who's asking in its own way, and then comes here, so that the
// two can't drift apart.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/logring"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/pkg/client"
)

// Errors a caller turns into its own kind of refusal.
var (
	ErrAdminOnly   = errors.New("only an admin can do this")
	ErrSourceInUse = errors.New("a read still uses this source")
)

const statePaused = "paused"

// App does things.
type App struct {
	Store     *store.Store
	Reads     *reads.Service
	Catalogue *replica.Client
	Sync      *replica.Syncer
	Logger    *slog.Logger
	// LogRing is what the server has logged lately. Nil keeps nothing.
	LogRing *logring.Ring
}

// caughtUp brings the copy up to date after a change to the catalogue, so that the
// caller can read its own write. The change has already happened, so a failure here
// only means it shows up a little later.
func (a *App) caughtUp(ctx context.Context) {
	if err := a.Sync.CatchUp(ctx); err != nil {
		a.Logger.Warn("could not catch up with the catalogue", "error", err)
	}
}

// demandChanged tells the catalogue straight away, so that a new read doesn't wait
// for the next round. The next round fixes a failure, so a read doesn't depend on it.
func (a *App) demandChanged(ctx context.Context, sourceID int64) {
	if err := a.Sync.PushDemand(ctx, sourceID); err != nil {
		a.Logger.Warn("could not report demand", "source", sourceID, "error", err)
	}
}

// startIfNew starts a paused source that has never been crawled, which is what an OPML
// import leaves behind. Only admins can change a source, so otherwise whoever imported
// it couldn't start it. A read is the decision to follow it. A source that's been
// crawled was paused on purpose, so it stays paused. The read exists either way, so a
// failure here is only logged.
func (a *App) startIfNew(ctx context.Context, sourceID int64) {
	source, err := a.Store.GetSource(ctx, sourceID)
	if err != nil || source.State != statePaused || source.LastCrawledAt != nil {
		return
	}
	state := client.SourcePatchStateIndexing
	if _, err := a.Catalogue.UpdateSource(ctx, sourceID, client.SourcePatch{State: &state}); err != nil {
		a.Logger.Warn("could not start a new source", "source", sourceID, "error", err)
		return
	}
	a.caughtUp(ctx)
}

// CreateRead makes a read, starts its source if nobody has before, and reports demand.
func (a *App) CreateRead(ctx context.Context, params reads.CreateParams) (reads.View, error) {
	created, err := a.Reads.Create(ctx, params)
	if err != nil {
		return reads.View{}, err
	}
	a.startIfNew(ctx, created.Read.SourceID)
	a.demandChanged(ctx, created.Read.SourceID)
	return created, nil
}

// DeleteRead deletes a read and reports the change in demand.
func (a *App) DeleteRead(ctx context.Context, actor store.Actor, id int64) error {
	found, err := a.Reads.Get(ctx, actor, id)
	if err != nil {
		return err
	}
	if err := a.Reads.Delete(ctx, actor, id); err != nil {
		return err
	}
	a.demandChanged(ctx, found.Read.SourceID)
	return nil
}

// Adjust changes a read and reports the change in demand.
func (a *App) Adjust(ctx context.Context, actor store.Actor, id int64, adjustment schedule.Adjustment) (reads.View, error) {
	adjusted, err := a.Reads.Adjust(ctx, actor, id, adjustment)
	if err != nil {
		return reads.View{}, err
	}
	a.demandChanged(ctx, adjusted.Read.SourceID)
	return adjusted, nil
}

// CreateSource adds a source. Anyone can.
func (a *App) CreateSource(ctx context.Context, create replica.SourceCreate) (client.Source, error) {
	created, err := a.Catalogue.CreateSource(ctx, create)
	if err != nil {
		return client.Source{}, err
	}
	a.caughtUp(ctx)
	return created, nil
}

// UpdateSource changes a source. Sources are shared, so only an admin can.
func (a *App) UpdateSource(ctx context.Context, actor store.Actor, id int64, patch client.SourcePatch) (client.Source, error) {
	if !actor.Admin {
		return client.Source{}, ErrAdminOnly
	}
	updated, err := a.Catalogue.UpdateSource(ctx, id, patch)
	if err != nil {
		return client.Source{}, err
	}
	a.caughtUp(ctx)
	return updated, nil
}

// DeleteSource deletes a source nobody reads. Only an admin can.
func (a *App) DeleteSource(ctx context.Context, actor store.Actor, id int64) error {
	if !actor.Admin {
		return ErrAdminOnly
	}
	count, err := a.Store.CountReads(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrSourceInUse
	}
	// The catalogue refuses while it thinks there's demand, which could be left over
	// from a read that's gone.
	if err := a.Catalogue.SetDemand(ctx, id, nil); err != nil {
		return err
	}
	if err := a.Catalogue.DeleteSource(ctx, id); err != nil {
		return err
	}
	a.caughtUp(ctx)
	return nil
}

// RequestCrawl asks for a crawl now. Anyone can.
func (a *App) RequestCrawl(ctx context.Context, id int64) error {
	if err := a.Catalogue.RequestCrawl(ctx, id); err != nil {
		return err
	}
	a.caughtUp(ctx)
	return nil
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

const (
	maxSlugBytes = 64
	slugAttempts = 50
)

// SlugOf makes a slug from a title.
func SlugOf(title string) string {
	slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > maxSlugBytes-4 { // Leaves room for a suffix.
		slug = strings.Trim(slug[:maxSlugBytes-4], "-")
	}
	if slug == "" {
		return "source"
	}
	return slug
}

// ErrDuplicate is a source that already exists, under another config or slug.
type ErrDuplicate struct{ SourceID int64 }

func (e *ErrDuplicate) Error() string { return "this serial is already a source" }

// CreateSourceNamed adds a source with a slug made from its title, adding a number
// when the slug is taken. A source that already exists is an *ErrDuplicate.
func (a *App) CreateSourceNamed(ctx context.Context, create replica.SourceCreate) (client.Source, error) {
	base := SlugOf(create.Title)
	for attempt := 1; attempt <= slugAttempts; attempt++ {
		create.Slug = base
		if attempt > 1 {
			create.Slug += "-" + strconv.Itoa(attempt)
		}
		created, err := a.CreateSource(ctx, create)
		var p *httpapi.Error
		switch {
		case err == nil:
			return created, nil
		case !errors.As(err, &p) || p.Problem.Status != http.StatusConflict:
			return client.Source{}, err
		case p.Problem.ExistingSourceID != nil:
			return client.Source{}, &ErrDuplicate{SourceID: *p.Problem.ExistingSourceID}
		}
	}
	return client.Source{}, errors.New("no free slug for this title")
}
