// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/feed"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/pkg/client"
)

const (
	maxOPMLBytes = 5 << 20
	maxSlugBytes = 64
	slugAttempts = 50

	statePaused = "paused"
)

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugOf makes a slug from a title.
func slugOf(title string) string {
	slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > maxSlugBytes-4 { // Leaves room for a suffix.
		slug = strings.Trim(slug[:maxSlugBytes-4], "-")
	}
	if slug == "" {
		return "feed"
	}
	return slug
}

// ImportOPML creates a paused source for each feed, so that nothing crawls until
// someone has probed and enabled it. One bad outline does not stop the rest.
func (h *handler) ImportOPML(ctx context.Context, r gen.ImportOPMLRequestObject) (gen.ImportOPMLResponseObject, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxOPMLBytes))
	if err != nil {
		return nil, fmt.Errorf("read OPML: %w", err)
	}
	outlines, err := feed.ParseOPML(body)
	if err != nil {
		return nil, invalidRequest("this is not an OPML document")
	}

	results := make([]client.OPMLOutlineResult, 0, len(outlines))
	inFile := map[string]bool{}
	for _, outline := range outlines {
		result := client.OPMLOutlineResult{Title: &outline.Title, FeedURL: &outline.FeedURL}
		switch {
		case inFile[outline.FeedURL]:
			result.Status = client.OPMLOutlineResultStatusDuplicate
		default:
			inFile[outline.FeedURL] = true
			h.importOutline(ctx, outline, &result)
		}
		results = append(results, result)
	}
	return gen.ImportOPML200JSONResponse{Results: results}, nil
}

func configFor(feedURL string) (client.SourceConfig, error) {
	var config client.SourceConfig
	err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: feedURL})
	return config, err
}

func (h *handler) importOutline(ctx context.Context, outline feed.Outline, result *client.OPMLOutlineResult) {
	invalid := func(detail string) {
		result.Status, result.Detail = client.OPMLOutlineResultStatusInvalid, &detail
	}
	config, err := configFor(outline.FeedURL)
	if err != nil {
		invalid(err.Error())
		return
	}

	existing, err := h.store.FindSourceByFeedURL(ctx, outline.FeedURL)
	if err == nil {
		result.Status, result.SourceID = client.OPMLOutlineResultStatusDuplicate, &existing.ID
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		invalid("the database could not be read")
		return
	}

	title, home := outline.Title, outline.HomeURL
	if title == "" {
		title = outline.FeedURL
	}
	if home == "" {
		home = outline.FeedURL
	}
	base := slugOf(title)
	for attempt := 1; attempt <= slugAttempts; attempt++ {
		slug := base
		if attempt > 1 {
			slug += "-" + strconv.Itoa(attempt)
		}
		created, err := h.app.CreateSource(ctx, replica.SourceCreate{
			SourceCreate: client.SourceCreate{Slug: slug, Title: title, HomeURL: home, DefaultKind: client.Article, Config: config},
			State:        statePaused,
		})
		var p *httpapi.Error
		switch {
		case err == nil:
			result.Status, result.SourceID = client.OPMLOutlineResultStatusCreated, &created.ID
			return
		case !errors.As(err, &p):
			invalid("the source could not be saved")
			return
		case p.Problem.ExistingSourceID != nil:
			result.Status, result.SourceID = client.OPMLOutlineResultStatusDuplicate, p.Problem.ExistingSourceID
			return
		case p.Problem.Status == http.StatusConflict:
			continue
		case p.Problem.Detail != nil:
			invalid(*p.Problem.Detail)
			return
		default:
			invalid(p.Problem.Title)
			return
		}
	}
	invalid("no free slug for this title")
}
