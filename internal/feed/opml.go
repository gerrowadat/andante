// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import "encoding/xml"

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
