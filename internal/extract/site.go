// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// feedLinks finds the links that mean a feed.
const feedLinks = `link[rel~="alternate"][type="application/rss+xml"], ` +
	`link[rel~="alternate"][type="application/atom+xml"], ` +
	`link[rel~="alternate"][type="application/feed+json"], ` +
	`link[rel~="alternate"][type="application/json"]`

// Site is what a site's home page says about the site itself.
type Site struct {
	Title string
	Feeds []string // In the order the page gives them.
	First string   // The first page of the archive, if the page links to it.
}

// ReadSite reads a home page.
func ReadSite(page Page) (Site, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Body))
	if err != nil {
		return Site{}, fmt.Errorf("parse %s: %w", page.URL, err)
	}
	var site Site
	site.Title, _ = doc.Find(`meta[property="og:site_name"]`).First().Attr("content")
	if site.Title = strings.TrimSpace(site.Title); site.Title == "" {
		site.Title = strings.TrimSpace(doc.Find("title").First().Text())
	}
	doc.Find(feedLinks).Each(func(_ int, link *goquery.Selection) {
		if href, ok := link.Attr("href"); ok && strings.TrimSpace(href) != "" {
			site.Feeds = append(site.Feeds, absoluteURL(strings.TrimSpace(href), page.URL))
		}
	})
	first, ok := doc.Find(`link[rel~="first"], a[rel~="first"]`).First().Attr("href")
	if !ok {
		first, _ = doc.Find(platformFirst).First().Attr("href")
	}
	if first = strings.TrimSpace(first); first != "" {
		site.First = absoluteURL(first, page.URL)
	}
	return site, nil
}
