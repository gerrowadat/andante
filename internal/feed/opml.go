// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/xml"
	"fmt"
)

// Outline is one feed in an OPML export.
type Outline struct {
	Title   string
	FeedURL string
	HomeURL string
}

type opmlDocument struct {
	XMLName xml.Name      `xml:"opml"`
	Version string        `xml:"version,attr"`
	Title   string        `xml:"head>title"`
	Items   []opmlOutline `xml:"body>outline"`
}

type opmlOutline struct {
	Text    string `xml:"text,attr"`
	Title   string `xml:"title,attr"`
	Type    string `xml:"type,attr"`
	XMLURL  string `xml:"xmlUrl,attr"`
	HTMLURL string `xml:"htmlUrl,attr"`
}

// OPML renders an OPML 2.0 list of feeds.
func OPML(outlines []Outline) ([]byte, error) {
	doc := opmlDocument{Version: "2.0", Title: "andante"}
	for _, o := range outlines {
		doc.Items = append(doc.Items, opmlOutline{Text: o.Title, Title: o.Title, Type: "rss", XMLURL: o.FeedURL, HTMLURL: o.HomeURL})
	}
	return marshalXML(doc)
}

// ParseOPML returns every feed in an OPML document, including those in folders.
func ParseOPML(body []byte) ([]Outline, error) {
	var doc struct {
		Items []parsedOutline `xml:"body>outline"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse OPML: %w", err)
	}
	var outlines []Outline
	var walk func(items []parsedOutline)
	walk = func(items []parsedOutline) {
		for _, item := range items {
			if len(item.Children) > 0 && item.XMLURL == "" {
				walk(item.Children)
				continue
			}
			title := item.Title
			if title == "" {
				title = item.Text
			}
			outlines = append(outlines, Outline{Title: title, FeedURL: item.XMLURL, HomeURL: item.HTMLURL})
		}
	}
	walk(doc.Items)
	return outlines, nil
}

type parsedOutline struct {
	Text     string          `xml:"text,attr"`
	Title    string          `xml:"title,attr"`
	XMLURL   string          `xml:"xmlUrl,attr"`
	HTMLURL  string          `xml:"htmlUrl,attr"`
	Children []parsedOutline `xml:"outline"`
}
