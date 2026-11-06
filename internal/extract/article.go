// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"codeberg.org/readeck/go-readability/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/microcosm-cc/bluemonday"
)

// articleNext finds post navigation in WordPress and Ghost themes.
const articleNext = ".nav-next a, .post-navigation .next a, a.next-post, .post-nav .next a, .gh-post-nav-next a"

// sanitiser keeps what an article needs and drops scripts, event handlers and
// iframes. Stored content ends up in everyone's feed reader, so nothing from a page
// or a feed reaches content_html without passing through it.
var sanitiser = bluemonday.UGCPolicy()

// Sanitise makes HTML from a page or a feed safe to store.
func Sanitise(html string) string { return sanitiser.Sanitize(html) }

// articleExtractor handles text-led pages.
type articleExtractor struct{}

func (articleExtractor) Extract(page Page, overrides Overrides) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Body))
	if err != nil {
		return Result{}, fmt.Errorf("parse page: %w", err)
	}
	var patterns Fields
	patterns.Next, _ = doc.Find(articleNext).First().Attr("href")
	if value, ok := doc.Find("time[datetime]").First().Attr("datetime"); ok {
		patterns.Date = parseDate(value)
	}
	fields, warnings := common(doc, page, overrides, patterns)
	if overrides.FullText {
		content, err := mainText(page)
		if err != nil {
			warnings = append(warnings, "no main text found")
		}
		fields.ContentHTML = content
	}
	return Result{Fields: fields, Warnings: warnings}, nil
}

func mainText(page Page) (string, error) {
	base, err := url.Parse(page.URL)
	if err != nil {
		return "", fmt.Errorf("parse page URL: %w", err)
	}
	article, err := readability.FromReader(bytes.NewReader(page.Body), base)
	if err != nil {
		return "", fmt.Errorf("find main text: %w", err)
	}
	var out strings.Builder
	if err := article.RenderHTML(&out); err != nil {
		return "", fmt.Errorf("render main text: %w", err)
	}
	return Sanitise(out.String()), nil
}
