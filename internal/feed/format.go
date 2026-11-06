// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"strings"
)

// Format is an output format. To add one, add it to formats.
type Format struct {
	Name        string // Also the extension of its URL.
	ContentType string
	Render      func(f Feed, selfURL string) ([]byte, error)
}

var formats = map[string]Format{
	"atom": {Name: "atom", ContentType: "application/atom+xml", Render: renderAtom},
	"rss":  {Name: "rss", ContentType: "application/rss+xml", Render: renderRSS},
	"json": {Name: "json", ContentType: "application/feed+json", Render: renderJSON},
}

// FormatFor returns the format with the given name.
func FormatFor(name string) (Format, bool) {
	f, ok := formats[name]
	return f, ok
}

// URL returns the feed URL of a token in a format.
func URL(baseURL, token, format string) string {
	return strings.TrimRight(baseURL, "/") + "/f/" + token + "." + format
}

// Suggested returns the format to offer first: podcast apps expect RSS.
func Suggested(defaultKind string) string {
	if defaultKind == kindEpisode {
		return "rss"
	}
	return "atom"
}
