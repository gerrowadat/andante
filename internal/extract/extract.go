// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"fmt"
	"time"
)

// Page is a fetched document.
type Page struct {
	URL  string
	Body []byte
}

// Fields is what an extractor found. Kinds fill in what applies to them.
type Fields struct {
	Title       string
	Summary     string
	ContentHTML string
	Images      []string
	AltText     string
	Enclosures  []Enclosure
	Date        *time.Time
	Next        string
	Prev        string
	media       string // What JSON-LD's associatedMedia points at.
}

// Enclosure is a media file on a page. A zero Type or Length means the page did
// not say.
type Enclosure struct {
	URL    string
	Type   string
	Length int64
}

// Result is a page's fields, and anything a person should check about them.
type Result struct {
	Fields   Fields
	Warnings []string
}

// Overrides are CSS selectors from a source's config. When set, they win.
type Overrides struct {
	Title   string
	Image   string
	AltText string
	Date    string
	Next    string
	// FullText stores an article's main text, which is opt-in per source. Where a
	// publisher's feed gives only a summary, the publisher chose that.
	FullText bool
}

// Extractor pulls fields out of a page. Each entry kind has its own.
type Extractor interface {
	Extract(page Page, overrides Overrides) (Result, error)
}

var extractors = map[string]Extractor{
	"page":    pageExtractor{},
	"article": articleExtractor{},
	"episode": episodeExtractor{},
}

// For returns the extractor for an entry kind.
func For(kind string) (Extractor, error) {
	e, ok := extractors[kind]
	if !ok {
		return nil, fmt.Errorf("no extractor for kind %q yet", kind)
	}
	return e, nil
}
