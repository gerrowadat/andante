// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"bytes"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// episodeExtractor handles media-led pages. It only finds where the media is, and
// andante never downloads it.
type episodeExtractor struct{}

func (episodeExtractor) Extract(page Page, overrides Overrides) (Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Body))
	if err != nil {
		return Result{}, fmt.Errorf("parse page: %w", err)
	}
	var patterns Fields
	patterns.Next, _ = doc.Find(articleNext).First().Attr("href")
	fields, warnings := common(doc, page, overrides, patterns)

	// associatedMedia is a picture on a comic and the audio here.
	fields.Images = slices.DeleteFunc(fields.Images, func(image string) bool {
		return fields.media != "" && strings.HasSuffix(image, strings.TrimPrefix(fields.media, "."))
	})
	fields.Enclosures = enclosures(doc, fields.media, page.URL)
	if len(fields.Enclosures) == 0 {
		warnings = append(warnings, "no audio or video found")
	}
	return Result{Fields: fields, Warnings: warnings}, nil
}

func enclosures(doc *goquery.Document, media, pageURL string) []Enclosure {
	var found []Enclosure
	add := func(link, mediaType string) {
		absolute := absoluteURL(link, pageURL)
		if absolute == "" || slices.ContainsFunc(found, func(e Enclosure) bool { return e.URL == absolute }) {
			return
		}
		found = append(found, Enclosure{URL: absolute, Type: mediaType})
	}
	if media != "" {
		add(media, "")
	}
	doc.Find("audio[src], video[src], audio source[src], video source[src]").Each(func(_ int, s *goquery.Selection) {
		link, _ := s.Attr("src")
		add(link, s.AttrOr("type", ""))
	})
	return found
}

func absoluteURL(link, pageURL string) string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	u, err := base.Parse(strings.TrimSpace(link))
	if err != nil {
		return ""
	}
	return u.String()
}
