// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// pageExtractor handles image-led pages, such as a comic's. Each source below
// fills only what the ones before it left empty, in the order open metadata
// first, heuristics after. Selector overrides win over all of them.
type pageExtractor struct{}

// Selectors for platforms: ComicControl (Hiveworks), then the WordPress comic
// plugins Webcomic, Toocheke and ComicPress/ComicEasel.
const (
	platformImage = "#cc-comic, #cc-comicbody img, .webcomic-image img, #comic img, .comic-image img, #spliced-comic img"
	platformNext  = "a.cc-next, a.next-comic, .webcomic-link.next-webcomic-link a, a.next-webcomic-link, a.comic-nav-next, .comic-nav-next a, a.navi-next, a.next"
	platformFirst = "a.cc-first, a.first-comic, a.first-webcomic-link, a.comic-nav-first, .webcomic-link.first-webcomic-link a"
	platformPrev  = "a.cc-prev, a.previous-comic, a.previous-webcomic-link, a.comic-nav-previous, .comic-nav-previous a, a.navi-prev, a.prev"
)

func (pageExtractor) Extract(page Page, overrides Overrides) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Body))
	if err != nil {
		return Result{}, fmt.Errorf("parse page: %w", err)
	}
	fields, warnings := common(doc, page, overrides, platform(doc))
	if len(fields.Images) == 0 {
		warnings = append(warnings, "no image found")
	}
	if fields.AltText == "" {
		fields.AltText = altText(doc, fields.Images)
	}
	return Result{Fields: fields, Warnings: warnings}, nil
}

// common runs what every kind shares: open metadata first, then the kind's own
// patterns, and overrides over all of them.
func common(doc *goquery.Document, page Page, overrides Overrides, patterns Fields) (Fields, []string) {
	fields, warnings := jsonLD(doc)
	merge(&fields, openGraph(doc))
	merge(&fields, linkRelations(doc))
	merge(&fields, patterns)
	merge(&fields, Fields{Title: strings.TrimSpace(doc.Find("title").First().Text())})

	overridden := selected(doc, overrides)
	fields = overlay(fields, overridden)
	warnings = append(warnings, resolve(&fields, page.URL)...)

	if fields.Next != "" && sameDocument(fields.Next, page.URL) {
		warnings = append(warnings, "next link points to the current page")
	}
	return fields, warnings
}

// merge fills dst's empty fields from src.
func merge(dst *Fields, src Fields) {
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if dst.ContentHTML == "" {
		dst.ContentHTML = src.ContentHTML
	}
	if dst.media == "" {
		dst.media = src.media
	}
	if len(dst.Enclosures) == 0 {
		dst.Enclosures = src.Enclosures
	}
	if dst.Summary == "" {
		dst.Summary = src.Summary
	}
	if len(dst.Images) == 0 {
		dst.Images = src.Images
	}
	if dst.AltText == "" {
		dst.AltText = src.AltText
	}
	if dst.Date == nil {
		dst.Date = src.Date
	}
	if dst.Next == "" {
		dst.Next = src.Next
	}
	if dst.Prev == "" {
		dst.Prev = src.Prev
	}
}

// overlay returns base with every field src set.
func overlay(base, src Fields) Fields {
	merge(&src, base)
	return src
}

func openGraph(doc *goquery.Document) Fields {
	meta := func(property string) string {
		value, _ := doc.Find(`meta[property="` + property + `"]`).First().Attr("content")
		return strings.TrimSpace(value)
	}
	f := Fields{Title: meta("og:title"), Summary: meta("og:description")}
	if image := meta("og:image"); image != "" {
		f.Images = []string{image}
	}
	if published := meta("article:published_time"); published != "" {
		f.Date = parseDate(published)
	}
	return f
}

func linkRelations(doc *goquery.Document) Fields {
	rel := func(name string) string {
		href, _ := doc.Find(`link[rel~="` + name + `"], a[rel~="` + name + `"]`).First().Attr("href")
		return strings.TrimSpace(href)
	}
	return Fields{Next: rel("next"), Prev: rel("prev")}
}

func platform(doc *goquery.Document) Fields {
	var f Fields
	if image := doc.Find(platformImage).First(); image.Length() > 0 {
		if src, ok := image.Attr("src"); ok {
			f.Images = []string{strings.TrimSpace(src)}
		}
	}
	f.Next, _ = doc.Find(platformNext).First().Attr("href")
	f.Prev, _ = doc.Find(platformPrev).First().Attr("href")
	if value, ok := doc.Find("time[datetime]").First().Attr("datetime"); ok {
		f.Date = parseDate(value)
	}
	return f
}

func selected(doc *goquery.Document, o Overrides) Fields {
	var f Fields
	if o.Title != "" {
		f.Title = strings.TrimSpace(doc.Find(o.Title).First().Text())
	}
	if o.Image != "" {
		if src, ok := doc.Find(o.Image).First().Attr("src"); ok {
			f.Images = []string{strings.TrimSpace(src)}
		}
	}
	if o.AltText != "" {
		selection := doc.Find(o.AltText).First()
		f.AltText = selection.AttrOr("title", strings.TrimSpace(selection.Text()))
	}
	if o.Date != "" {
		selection := doc.Find(o.Date).First()
		f.Date = parseDate(selection.AttrOr("datetime", selection.Text()))
	}
	if o.Next != "" {
		f.Next = doc.Find(o.Next).First().AttrOr("href", "")
	}
	return f
}

// altText reads the title attribute of the main image, which is where comics
// such as xkcd keep it.
func altText(doc *goquery.Document, images []string) string {
	if len(images) == 0 {
		return ""
	}
	var title string
	doc.Find("img[title]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		src, _ := s.Attr("src")
		if src != "" && strings.HasSuffix(images[0], strings.TrimPrefix(src, ".")) {
			title, _ = s.Attr("title")
			return false
		}
		return true
	})
	return strings.TrimSpace(title)
}

// resolve makes every link absolute against the page.
func resolve(f *Fields, pageURL string) []string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return []string{"the page URL could not be parsed, so links stay relative"}
	}
	absolute := func(link string) string {
		if link == "" {
			return ""
		}
		u, err := base.Parse(strings.TrimSpace(link))
		if err != nil {
			return ""
		}
		u.Fragment = ""
		return u.String()
	}
	f.Next, f.Prev = absolute(f.Next), absolute(f.Prev)
	images := make([]string, 0, len(f.Images))
	for _, image := range f.Images {
		if resolved := absolute(image); resolved != "" {
			images = append(images, resolved)
		}
	}
	f.Images = images
	return nil
}

func sameDocument(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}
