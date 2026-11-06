// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/extract"
)

// Crawl walks a source's pages by their next link.
type Crawl struct{}

type crawlConfig struct {
	Strategy  string `json:"strategy"`
	StartURL  string `json:"start_url"`
	Selectors struct {
		Title   string `json:"title"`
		Image   string `json:"image"`
		AltText string `json:"alt_text"`
		Date    string `json:"date"`
		Next    string `json:"next"`
	} `json:"selectors"`
	FullText bool `json:"full_text"`
}

// crawlCursor is where a crawl has got to. Next is the page to fetch. With no
// Next the crawl is at the live end, and Last is the page to re-check.
type crawlCursor struct {
	Next string `json:"next,omitempty"`
	Last string `json:"last,omitempty"`
}

// Name implements discovery.Strategy.
func (Crawl) Name() string { return "crawl" }

func decodeConfig(raw json.RawMessage) (crawlConfig, error) {
	var c crawlConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return crawlConfig{}, fmt.Errorf("decode crawl config: %w", err)
	}
	return c, nil
}

// Validate implements discovery.Strategy.
func (Crawl) Validate(raw json.RawMessage) error {
	c, err := decodeConfig(raw)
	if err != nil {
		return err
	}
	u, err := url.Parse(c.StartURL)
	if err != nil {
		return fmt.Errorf("start_url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("start_url must be an absolute http(s) URL")
	}
	for name, selector := range map[string]string{
		"title": c.Selectors.Title, "image": c.Selectors.Image, "alt_text": c.Selectors.AltText,
		"date": c.Selectors.Date, "next": c.Selectors.Next,
	} {
		if selector == "" {
			continue
		}
		if _, err := cascadia.ParseGroup(selector); err != nil {
			return fmt.Errorf("selectors.%s is not a valid CSS selector: %w", name, err)
		}
	}
	return nil
}

func overridesOf(c crawlConfig) extract.Overrides {
	s := c.Selectors
	return extract.Overrides{
		Title: s.Title, Image: s.Image, AltText: s.AltText, Date: s.Date, Next: s.Next, FullText: c.FullText,
	}
}

// crawl is one call to Next.
type crawl struct {
	fetcher   discovery.PageFetcher
	extractor extract.Extractor
	overrides extract.Overrides
	kind      string
	urls      map[string]bool
	hashes    map[string]bool
	batch     discovery.Batch
}

// Next implements discovery.Strategy.
func (Crawl) Next(ctx context.Context, fetcher discovery.PageFetcher, src discovery.Source, cur discovery.Cursor, limit int) (discovery.Batch, error) {
	config, err := decodeConfig(src.Config)
	if err != nil {
		return discovery.Batch{}, err
	}
	extractor, err := extract.For(src.DefaultKind)
	if err != nil {
		return discovery.Batch{}, err
	}
	var cursor crawlCursor
	if len(cur) > 0 {
		if err := json.Unmarshal(cur, &cursor); err != nil {
			return discovery.Batch{}, fmt.Errorf("decode crawl cursor: %w", err)
		}
	}
	if cursor.Next == "" && cursor.Last == "" {
		cursor.Next = config.StartURL
	}

	c := &crawl{
		fetcher:   fetcher,
		extractor: extractor,
		overrides: overridesOf(config),
		kind:      src.DefaultKind,
		urls:      map[string]bool{},
		hashes:    map[string]bool{},
	}
	if cursor.Next == "" {
		if err := c.recheck(ctx, &cursor); err != nil {
			return discovery.Batch{}, err
		}
	}
	for cursor.Next != "" && len(c.batch.Entries) < limit {
		if err := c.step(ctx, &cursor); err != nil {
			// Keep what this call found. The next call retries from here, and
			// meets the error then if it still stands.
			if errors.Is(err, discovery.ErrCycle) || len(c.batch.Entries) == 0 {
				return discovery.Batch{}, err
			}
			break
		}
	}
	c.batch.LiveEnd = cursor.Next == ""

	encoded, err := json.Marshal(cursor)
	if err != nil {
		return discovery.Batch{}, fmt.Errorf("encode crawl cursor: %w", err)
	}
	c.batch.Cursor = encoded
	return c.batch, nil
}

// recheck re-fetches the last page, to see whether a next link has appeared and
// to pick up any change to the page itself.
func (c *crawl) recheck(ctx context.Context, cursor *crawlCursor) error {
	resp, err := c.fetcher.Revalidate(ctx, cursor.Last)
	if err != nil {
		return err
	}
	if resp.NotModified {
		return nil
	}
	result, err := c.extract(resp)
	if err != nil {
		return err
	}
	c.batch.Updates = append(c.batch.Updates, c.entry(ctx, cursor.Last, result))
	if next := result.Fields.Next; next != "" && !same(next, cursor.Last) {
		cursor.Next = next
	}
	return nil
}

// step fetches the next page and moves the cursor past it.
func (c *crawl) step(ctx context.Context, cursor *crawlCursor) error {
	current := cursor.Next
	if c.urls[current] {
		return fmt.Errorf("%w: back at %s", discovery.ErrCycle, current)
	}
	resp, err := c.fetcher.Fetch(ctx, current)
	if err != nil {
		return err
	}
	result, err := c.extract(resp)
	if err != nil {
		return err
	}
	entry := c.entry(ctx, resp.URL, result)
	if entry.ContentHash != nil {
		key := string(entry.ContentHash)
		if c.hashes[key] {
			return fmt.Errorf("%w: %s repeats a page already seen", discovery.ErrCycle, current)
		}
		c.hashes[key] = true
	}
	c.urls[current], c.urls[resp.URL] = true, true
	c.batch.Entries = append(c.batch.Entries, entry)
	c.batch.Warnings = append(c.batch.Warnings, prefixed(resp.URL, result.Warnings)...)

	cursor.Last, cursor.Next = resp.URL, ""
	switch next := result.Fields.Next; {
	case next == "":
		c.batch.Warnings = append(c.batch.Warnings, "no next link on "+resp.URL+"; treating it as the live end")
	case same(next, resp.URL):
	default:
		cursor.Next = next
	}
	return nil
}

func (c *crawl) extract(resp *discovery.Response) (extract.Result, error) {
	result, err := c.extractor.Extract(extract.Page{URL: resp.URL, Body: resp.Body}, c.overrides)
	if err != nil {
		return extract.Result{}, fmt.Errorf("extract %s: %w", resp.URL, err)
	}
	return result, nil
}

func (c *crawl) entry(ctx context.Context, pageURL string, result extract.Result) discovery.Entry {
	f := result.Fields
	return discovery.Entry{
		Kind:        c.kind,
		URL:         pageURL,
		Title:       f.Title,
		Summary:     f.Summary,
		ContentHTML: f.ContentHTML,
		Images:      f.Images,
		AltText:     f.AltText,
		Enclosures:  enclosuresOf(ctx, c.fetcher, f.Enclosures),
		Date:        f.Date,
		ContentHash: contentHash(f),
	}
}

// enclosuresOf fills in a media file's type and length with a HEAD request, only
// where the page did not give them. andante never downloads a file. A failed HEAD
// loses only those two fields, so it is not worth stopping for.
func enclosuresOf(ctx context.Context, fetcher discovery.PageFetcher, found []extract.Enclosure) []discovery.Enclosure {
	out := make([]discovery.Enclosure, 0, len(found))
	for _, e := range found {
		enclosure := discovery.Enclosure{URL: e.URL, Type: e.Type, Length: e.Length}
		if e.Type == "" || e.Length == 0 {
			if resp, err := fetcher.Head(ctx, e.URL); err == nil {
				if enclosure.Type == "" {
					enclosure.Type = resp.ContentType
				}
				if enclosure.Length == 0 {
					enclosure.Length = max(resp.ContentLength, 0)
				}
			}
		}
		out = append(out, enclosure)
	}
	return out
}

// contentHash identifies a page by what it shows, not its URL, so that a chain
// which comes back through a different URL is still caught. A page with neither
// a title nor an image has nothing to compare.
func contentHash(f extract.Fields) []byte {
	if f.Title == "" && len(f.Images) == 0 {
		return nil
	}
	hash := sha256.Sum256([]byte(f.Title + "\x00" + strings.Join(f.Images, "\x00")))
	return hash[:]
}

func same(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

func prefixed(pageURL string, warnings []string) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w+" on "+pageURL)
	}
	return out
}
