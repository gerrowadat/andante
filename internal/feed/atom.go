// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/xml"
	"fmt"
	"time"
)

// Atom, RFC 4287. There is no feed library here because the ones available drop
// the itunes: and podcast: fields podcast apps need, so all the formats are plain
// structs over the standard library.

type atomFeed struct {
	XMLName   xml.Name      `xml:"http://www.w3.org/2005/Atom feed"`
	ID        string        `xml:"id"`
	Title     string        `xml:"title"`
	Subtitle  string        `xml:"subtitle"`
	Updated   string        `xml:"updated"`
	Author    atomAuthor    `xml:"author"`
	Generator atomGenerator `xml:"generator"`
	Logo      string        `xml:"logo,omitempty"`
	Links     []atomLink    `xml:"link"`
	Entries   []atomEntry   `xml:"entry"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomGenerator struct {
	Version string `xml:"version,attr,omitempty"`
	Name    string `xml:",chardata"`
}

type atomLink struct {
	Rel    string `xml:"rel,attr"`
	Href   string `xml:"href,attr"`
	Type   string `xml:"type,attr,omitempty"`
	Length int64  `xml:"length,attr,omitempty"`
}

type atomContent struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type atomEntry struct {
	ID        string      `xml:"id"`
	Title     string      `xml:"title"`
	Updated   string      `xml:"updated"`
	Published string      `xml:"published"`
	Links     []atomLink  `xml:"link"`
	Content   atomContent `xml:"content"`
}

func atomTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func renderAtom(f Feed, selfURL string) ([]byte, error) {
	out := atomFeed{
		ID:        f.ID,
		Title:     f.Title,
		Subtitle:  f.Description,
		Updated:   atomTime(f.Updated),
		Author:    atomAuthor{Name: f.Author},
		Generator: atomGenerator{Version: f.Version, Name: f.Generator},
		Logo:      f.Image,
		Links: []atomLink{
			{Rel: "self", Href: selfURL, Type: "application/atom+xml"},
			{Rel: "alternate", Href: f.HomeURL},
		},
	}
	for _, item := range f.Items {
		entry := atomEntry{
			ID:        item.ID,
			Title:     item.Title,
			Updated:   atomTime(item.Published),
			Published: atomTime(item.Published),
			Content:   atomContent{Type: "html", Body: item.ContentHTML},
		}
		if item.Link != "" {
			entry.Links = append(entry.Links, atomLink{Rel: "alternate", Href: item.Link})
		}
		for _, e := range item.Enclosures {
			entry.Links = append(entry.Links, atomLink{Rel: "enclosure", Href: e.URL, Type: e.Type, Length: e.Length})
		}
		out.Entries = append(out.Entries, entry)
	}
	return marshalXML(out)
}

func marshalXML(v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode feed: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}
