// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/extract"
	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
)

const (
	defaultMaxPages  = 100
	defaultPageParam = "paged"

	// seenLength is how much of an item's identity hash a cursor keeps. Enough
	// that an archive cannot collide, short enough that the cursor stays small.
	seenLength = 8
)

// Feed reads a feed that holds the whole archive, or RFC 5005 archived feeds.
type Feed struct{}

// FeedPaged walks a WordPress-style feed that takes a page number.
type FeedPaged struct{}

// Name implements discovery.Strategy.
func (Feed) Name() string { return "feed" }

// Name implements discovery.Strategy.
func (FeedPaged) Name() string { return "feed-paged" }

type feedConfig struct {
	Strategy  string `json:"strategy"`
	FeedURL   string `json:"feed_url"`
	FillGaps  bool   `json:"fill_gaps"`
	FullText  bool   `json:"full_text"`
	MaxPages  int    `json:"max_pages"`
	PageParam string `json:"page_param"`
}

// feedCursor records what has been handed out, because a feed has no place to
// resume from: items can come and go at its head. Walked means the whole archive
// has been read, and from then on only the live feed is.
type feedCursor struct {
	Walked bool     `json:"walked"`
	Seen   []string `json:"seen"`
}

func decodeFeedConfig(raw json.RawMessage) (feedConfig, error) {
	var c feedConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return feedConfig{}, fmt.Errorf("decode feed config: %w", err)
	}
	u, err := url.Parse(c.FeedURL)
	if err != nil {
		return feedConfig{}, fmt.Errorf("feed_url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return feedConfig{}, errors.New("feed_url must be an absolute http(s) URL")
	}
	return c, nil
}

// Validate implements discovery.Strategy.
func (Feed) Validate(raw json.RawMessage) error {
	_, err := decodeFeedConfig(raw)
	return err
}

// Validate implements discovery.Strategy.
func (FeedPaged) Validate(raw json.RawMessage) error {
	_, err := decodeFeedConfig(raw)
	return err
}

// Next implements discovery.Strategy.
func (Feed) Next(ctx context.Context, fetcher discovery.PageFetcher, src discovery.Source, cur discovery.Cursor, limit int) (discovery.Batch, error) {
	return next(ctx, fetcher, src, cur, limit, walkArchives)
}

// Next implements discovery.Strategy.
func (FeedPaged) Next(ctx context.Context, fetcher discovery.PageFetcher, src discovery.Source, cur discovery.Cursor, limit int) (discovery.Batch, error) {
	return next(ctx, fetcher, src, cur, limit, walkPages)
}

// walker fetches every document of an archive.
type walker func(ctx context.Context, fetcher discovery.PageFetcher, config feedConfig) ([]*gofeed.Feed, error)

func parse(body []byte) (*gofeed.Feed, error) {
	parsed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return parsed, nil
}

// walkArchives reads a feed and the RFC 5005 archives behind it.
func walkArchives(ctx context.Context, fetcher discovery.PageFetcher, config feedConfig) ([]*gofeed.Feed, error) {
	var docs []*gofeed.Feed
	visited := map[string]bool{}
	for link := config.FeedURL; link != ""; {
		if visited[link] {
			return nil, fmt.Errorf("%w: back at %s", discovery.ErrCycle, link)
		}
		visited[link] = true
		resp, err := fetcher.Fetch(ctx, link)
		if err != nil {
			return nil, err
		}
		doc, err := parse(resp.Body)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
		link = archiveLink(resp.Body, resp.URL)
	}
	return docs, nil
}

// archiveLink finds rel="prev-archive" in a feed's head. gofeed keeps a link's
// target but not its rel, so this reads the XML itself.
func archiveLink(body []byte, base string) string {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "entry", "item":
			return ""
		case "link":
			var rel, href string
			for _, attr := range start.Attr {
				switch attr.Name.Local {
				case "rel":
					rel = attr.Value
				case "href":
					href = attr.Value
				}
			}
			if rel == "prev-archive" && href != "" {
				u, err := url.Parse(base)
				if err != nil {
					return href
				}
				target, err := u.Parse(href)
				if err != nil {
					return ""
				}
				return target.String()
			}
		}
	}
}

// walkPages reads ?paged=N until a 404, an empty page, a repeat or max_pages.
func walkPages(ctx context.Context, fetcher discovery.PageFetcher, config feedConfig) ([]*gofeed.Feed, error) {
	base, err := url.Parse(config.FeedURL)
	if err != nil {
		return nil, fmt.Errorf("feed_url: %w", err)
	}
	param, limit := config.PageParam, config.MaxPages
	if param == "" {
		param = defaultPageParam
	}
	if limit <= 0 {
		limit = defaultMaxPages
	}

	var docs []*gofeed.Feed
	first := ""
	for page := 1; page <= limit; page++ {
		u := *base
		query := u.Query()
		query.Set(param, strconv.Itoa(page))
		u.RawQuery = query.Encode()

		resp, err := fetcher.Fetch(ctx, u.String())
		var status *discovery.StatusError
		if errors.As(err, &status) && status.Status == http.StatusNotFound && page > 1 {
			break
		}
		if err != nil {
			return nil, err
		}
		doc, err := parse(resp.Body)
		if err != nil {
			return nil, err
		}
		if len(doc.Items) == 0 {
			break
		}
		// A site that ignores the parameter sends the same page for ever.
		identity := itemIdentity(doc.Items[0])
		if identity == first {
			break
		}
		if first == "" {
			first = identity
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, errors.New("the feed has no items")
	}
	return docs, nil
}

func itemIdentity(item *gofeed.Item) string {
	if item.GUID != "" {
		return item.GUID
	}
	if item.Link != "" {
		return item.Link
	}
	if len(item.Enclosures) > 0 {
		return item.Enclosures[0].URL
	}
	return ""
}

func seenKey(identity string) string {
	hash := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(hash[:seenLength])
}

func next(ctx context.Context, fetcher discovery.PageFetcher, src discovery.Source, cur discovery.Cursor, limit int, walk walker) (discovery.Batch, error) {
	config, err := decodeFeedConfig(src.Config)
	if err != nil {
		return discovery.Batch{}, err
	}
	var cursor feedCursor
	if len(cur) > 0 {
		if err := json.Unmarshal(cur, &cursor); err != nil {
			return discovery.Batch{}, fmt.Errorf("decode feed cursor: %w", err)
		}
	}

	var (
		batch discovery.Batch
		docs  []*gofeed.Feed
	)
	if cursor.Walked {
		resp, err := fetcher.Revalidate(ctx, config.FeedURL)
		if err != nil {
			return discovery.Batch{}, err
		}
		if resp.NotModified {
			batch.Cursor, batch.LiveEnd = cur, true
			return batch, nil
		}
		doc, err := parse(resp.Body)
		if err != nil {
			return discovery.Batch{}, err
		}
		docs = []*gofeed.Feed{doc}
	} else {
		if docs, err = walk(ctx, fetcher, config); err != nil {
			return discovery.Batch{}, err
		}
		batch.Source = sourceUpdate(docs[0])
		batch.Warnings = append(batch.Warnings, truncationWarning(docs)...)
	}

	seen := make(map[string]bool, len(cursor.Seen))
	for _, key := range cursor.Seen {
		seen[key] = true
	}
	var entries []discovery.Entry
	for _, doc := range docs {
		for _, item := range doc.Items {
			identity := itemIdentity(item)
			if identity == "" {
				batch.Warnings = append(batch.Warnings, "skipped an item with no guid, link or enclosure: "+item.Title)
				continue
			}
			key := seenKey(identity)
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, entryOf(item, src.DefaultKind))
		}
	}
	entries = oldestFirst(entries)

	// A feed has already been fetched whole, so handing it over whole costs nothing.
	// Filling gaps costs a request per entry, so that keeps to the limit.
	if config.FillGaps && len(entries) > limit {
		entries = entries[:limit]
	}
	if config.FillGaps {
		entries, batch.Warnings = fill(ctx, fetcher, config, entries, batch.Warnings)
	}

	handed := append([]string(nil), cursor.Seen...)
	for _, e := range entries {
		handed = append(handed, seenKey(entryIdentity(e)))
	}
	left := false
	remaining := map[string]bool{}
	for _, key := range handed {
		remaining[key] = true
	}
	for key := range seen {
		if !remaining[key] {
			left = true
		}
	}

	cursor.Seen = handed
	cursor.Walked = cursor.Walked || !left
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return discovery.Batch{}, fmt.Errorf("encode feed cursor: %w", err)
	}
	batch.Entries, batch.Cursor, batch.LiveEnd = entries, encoded, !left
	return batch, nil
}

func entryIdentity(e discovery.Entry) string {
	if e.GUID != "" {
		return e.GUID
	}
	return e.URL
}

// truncationWarning spots a feed that looks like the last few items and not the
// archive. Feeds are cut off at round numbers, and FeedRewind tells long feeds from
// short ones in much the same way.
func truncationWarning(docs []*gofeed.Feed) []string {
	if len(docs) != 1 {
		return nil
	}
	switch len(docs[0].Items) {
	case 10, 15, 20, 25, 30, 50, 100:
		return []string{fmt.Sprintf("the feed has exactly %d items, so it may hold only the latest; try feed-paged or crawl", len(docs[0].Items))}
	}
	return nil
}

// FeedInfo is what can be told about a feed from one look at it.
type FeedInfo struct {
	Title   string
	HomeURL string
	Items   int
	First   string // The identity of its first item, to tell pages apart.
	CutOff  bool   // It looks like the latest items, and not the archive.
}

// ReadFeed returns what's in a feed, or false if body isn't one with items in it.
func ReadFeed(body []byte, base string) (FeedInfo, bool) {
	doc, err := parse(body)
	if err != nil || len(doc.Items) == 0 {
		return FeedInfo{}, false
	}
	return FeedInfo{
		Title:   doc.Title,
		HomeURL: doc.Link,
		Items:   len(doc.Items),
		First:   itemIdentity(doc.Items[0]),
		CutOff:  len(truncationWarning([]*gofeed.Feed{doc})) > 0 && archiveLink(body, base) == "",
	}, true
}

func sourceUpdate(doc *gofeed.Feed) *discovery.SourceUpdate {
	update := &discovery.SourceUpdate{Extra: map[string]any{}}
	if doc.Image != nil {
		update.ImageURL = doc.Image.URL
	}
	if len(doc.Authors) > 0 && doc.Authors[0] != nil {
		update.Extra["author"] = doc.Authors[0].Name
	}
	if len(doc.Categories) > 0 {
		update.Extra["categories"] = doc.Categories
	}
	if itunes := doc.ITunesExt; itunes != nil {
		if itunes.Image != "" {
			update.ImageURL = itunes.Image
		}
		set(update.Extra, "itunes:author", itunes.Author)
		set(update.Extra, "itunes:explicit", itunes.Explicit)
		var categories []string
		for _, category := range itunes.Categories {
			categories = append(categories, category.Text)
		}
		if len(categories) > 0 {
			update.Extra["itunes:category"] = categories
		}
	}
	return update
}

func set(extra map[string]any, key, value string) {
	if value != "" {
		extra[key] = value
	}
}

func entryOf(item *gofeed.Item, defaultKind string) discovery.Entry {
	e := discovery.Entry{
		Kind:    defaultKind,
		URL:     item.Link,
		GUID:    item.GUID,
		Title:   item.Title,
		Summary: item.Description,
		Date:    item.PublishedParsed,
		Extra:   map[string]any{},
	}
	if e.Date == nil {
		e.Date = item.UpdatedParsed
	}
	if item.Content != "" {
		e.ContentHTML = extract.Sanitise(item.Content)
	}
	for _, enclosure := range item.Enclosures {
		if enclosure == nil || enclosure.URL == "" {
			continue
		}
		length, _ := strconv.ParseInt(enclosure.Length, 10, 64)
		e.Enclosures = append(e.Enclosures, discovery.Enclosure{URL: enclosure.URL, Type: enclosure.Type, Length: max(length, 0)})
		if strings.HasPrefix(enclosure.Type, "audio/") || strings.HasPrefix(enclosure.Type, "video/") {
			e.Kind = "episode"
		}
	}
	if e.URL == "" && len(e.Enclosures) > 0 {
		e.URL = e.Enclosures[0].URL
	}
	if item.Image != nil {
		e.Images = []string{item.Image.URL}
	}
	if itunes := item.ITunesExt; itunes != nil {
		e.Duration = parseDuration(itunes.Duration)
		set(e.Extra, "itunes:image", itunes.Image)
		set(e.Extra, "itunes:episode", itunes.Episode)
		set(e.Extra, "itunes:season", itunes.Season)
		set(e.Extra, "itunes:explicit", itunes.Explicit)
	}
	podcastExtra(e.Extra, item.Extensions)
	return e
}

func podcastExtra(extra map[string]any, extensions ext.Extensions) {
	for _, name := range []string{"transcript", "chapters"} {
		found := extensions["podcast"][name]
		if len(found) == 0 {
			continue
		}
		attrs := found[0].Attrs
		if attrs["url"] != "" {
			extra["podcast:"+name] = map[string]any{"url": attrs["url"], "type": attrs["type"]}
		}
	}
}

// parseDuration reads itunes:duration: seconds, MM:SS or HH:MM:SS.
func parseDuration(raw string) time.Duration {
	var total int64
	for part := range strings.SplitSeq(strings.TrimSpace(raw), ":") {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return time.Duration(total) * time.Second
}

// oldestFirst orders entries by date. If any lack one, dates cannot order them, and
// feeds run newest first, so the order is reversed instead.
func oldestFirst(entries []discovery.Entry) []discovery.Entry {
	slices.Reverse(entries)
	if slices.ContainsFunc(entries, func(e discovery.Entry) bool { return e.Date == nil }) {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Date.Before(*entries[j].Date) })
	return entries
}

// fill fetches entries' pages for what their feed items lacked. It does not touch
// what the feed gave, because the feed item is the primary record.
func fill(ctx context.Context, fetcher discovery.PageFetcher, config feedConfig, entries []discovery.Entry, warnings []string) ([]discovery.Entry, []string) {
	for i := range entries {
		e := &entries[i]
		if e.URL == "" {
			continue
		}
		extractor, err := extract.For(e.Kind)
		if err != nil {
			continue
		}
		resp, err := fetcher.Fetch(ctx, e.URL)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not fill %s: %v", e.URL, err))
			continue
		}
		result, err := extractor.Extract(extract.Page{URL: resp.URL, Body: resp.Body}, extract.Overrides{FullText: config.FullText})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not fill %s: %v", e.URL, err))
			continue
		}
		f := result.Fields
		if len(e.Images) == 0 {
			e.Images = f.Images
		}
		if e.AltText == "" {
			e.AltText = f.AltText
		}
		if e.ContentHTML == "" {
			e.ContentHTML = f.ContentHTML
		}
		if e.Date == nil {
			e.Date = f.Date
		}
	}
	return entries, warnings
}
