// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/store/db"
)

// Roles a feed token can have.
const (
	RoleOwner  = "owner"
	RoleViewer = "viewer"
)

// DefaultWindow is how many released entries a feed holds unless the read's
// config says otherwise. MaxWindow keeps a config from making feeds huge.
const (
	DefaultWindow = 30
	MaxWindow     = 500
)

// Enclosure is a media file attached to an item. Its URL passes through unchanged.
type Enclosure struct {
	URL    string `json:"url"`
	Type   string `json:"type"`
	Length int64  `json:"length"`
}

// Item is one entry of a feed, in no format yet.
type Item struct {
	ID          string
	Title       string
	Link        string
	Published   time.Time
	ContentHTML string
	Enclosures  []Enclosure
	Duration    time.Duration
	Image       string // Episode artwork.
	Extra       map[string]any
}

// Feed is a whole feed, in no format yet.
type Feed struct {
	ID          string
	Title       string
	Description string
	HomeURL     string
	Image       string
	Author      string
	Updated     time.Time
	Generator   string
	Version     string
	Extra       map[string]any
	Items       []Item
}

// Input is what a feed is built from.
type Input struct {
	Read    db.Read
	Source  db.Source
	Entries []reads.ReleasedEntry // Newest first.
	Role    string
	Token   string // The raw token, which only the owner's links contain.
	BaseURL string
	// Pace says how fast the read goes, in words.
	Pace string
}

// Window returns how many entries a read's feed holds.
func Window(read db.Read) int {
	var config struct {
		Window int `json:"window"`
	}
	if err := json.Unmarshal(read.Config, &config); err != nil || config.Window < 1 {
		return DefaultWindow
	}
	return min(config.Window, MaxWindow)
}

func decodeExtra(raw []byte) map[string]any {
	extra := map[string]any{}
	// Extra is pass-through metadata, so a bad value loses that and no more.
	_ = json.Unmarshal(raw, &extra)
	return extra
}

func text(extra map[string]any, key string) string {
	value, _ := extra[key].(string)
	return value
}

// tagBase is the part of an ID that names this read. The year is the read's own,
// so that IDs never change.
func tagBase(baseURL string, read db.Read) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	return fmt.Sprintf("tag:%s,%d:read/%d", u.Hostname(), read.CreatedAt.UTC().Year(), read.ID), nil
}

// Build makes the feed for one token: feeds are rendered per role.
func Build(in Input) (Feed, error) {
	tag, err := tagBase(in.BaseURL, in.Read)
	if err != nil {
		return Feed{}, err
	}
	extra := decodeExtra(in.Source.Extra)
	f := Feed{
		ID:    tag,
		Title: in.Source.Title + " — " + in.Read.Name,
		Description: "A paced replay of " + in.Source.Title + ", from " + in.Source.HomeUrl +
			". This feed is not the publisher's." + paceSentence(in.Pace),
		HomeURL:   in.Source.HomeUrl,
		Author:    in.Source.Title,
		Updated:   in.Read.CreatedAt,
		Generator: "andante",
		Version:   buildinfo.Version(),
		Extra:     extra,
		Items:     make([]Item, 0, len(in.Entries)),
	}
	if in.Source.ImageUrl != nil {
		f.Image = *in.Source.ImageUrl
	}
	if author := text(extra, "itunes:author"); author != "" {
		f.Author = author
	}
	if len(in.Entries) > 0 {
		f.Updated = in.Entries[0].At
	}

	for _, released := range in.Entries {
		entry := released.Entry
		item := Item{
			ID:        fmt.Sprintf("%s/entry/%d", tag, entry.ID),
			Title:     fmt.Sprintf("%s #%d", in.Source.Title, entry.Seq),
			Published: released.At,
			Extra:     decodeExtra(entry.Extra),
		}
		if entry.Title != nil && *entry.Title != "" {
			item.Title = *entry.Title
		}
		if entry.Url != nil {
			item.Link = *entry.Url
		}
		context := ItemContext{Read: in.Read, Entry: entry, Role: in.Role, Token: in.Token, BaseURL: in.BaseURL}
		for _, d := range decorators {
			if d.Applies(entry) {
				d.Decorate(context, &item)
			}
		}
		f.Items = append(f.Items, item)
	}
	return f, nil
}

func paceSentence(pace string) string {
	if pace == "" {
		return ""
	}
	return " Pace: " + pace + "."
}
