// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/xml"
	"strconv"
	"time"
)

const (
	namespaceAtom    = "http://www.w3.org/2005/Atom"
	namespaceITunes  = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	namespacePodcast = "https://podcastindex.org/namespace/1.0"
)

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	ITunes  string     `xml:"xmlns:itunes,attr"`
	Podcast string     `xml:"xmlns:podcast,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title          string           `xml:"title"`
	Link           string           `xml:"link"`
	Description    string           `xml:"description"`
	LastBuildDate  string           `xml:"lastBuildDate"`
	Generator      string           `xml:"generator"`
	Self           rssAtomLink      `xml:"atom:link"`
	Image          *rssImage        `xml:"image,omitempty"`
	ITunesAuthor   string           `xml:"itunes:author,omitempty"`
	ITunesImage    *rssITunesImage  `xml:"itunes:image,omitempty"`
	ITunesExplicit string           `xml:"itunes:explicit"`
	Categories     []rssITunesTopic `xml:"itunes:category,omitempty"`
	Items          []rssItem        `xml:"item"`
}

type rssAtomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssImage struct {
	URL   string `xml:"url"`
	Title string `xml:"title"`
	Link  string `xml:"link"`
}

type rssITunesImage struct {
	Href string `xml:"href,attr"`
}

type rssITunesTopic struct {
	Text string `xml:"text,attr"`
}

type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type rssPodcastLink struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr,omitempty"`
}

type rssItem struct {
	Title          string          `xml:"title"`
	Link           string          `xml:"link,omitempty"`
	GUID           rssGUID         `xml:"guid"`
	PubDate        string          `xml:"pubDate"`
	Description    string          `xml:"description"`
	Enclosures     []rssEnclosure  `xml:"enclosure,omitempty"`
	ITunesDuration string          `xml:"itunes:duration,omitempty"`
	ITunesImage    *rssITunesImage `xml:"itunes:image,omitempty"`
	ITunesEpisode  string          `xml:"itunes:episode,omitempty"`
	ITunesSeason   string          `xml:"itunes:season,omitempty"`
	ITunesExplicit string          `xml:"itunes:explicit,omitempty"`
	Transcript     *rssPodcastLink `xml:"podcast:transcript,omitempty"`
	Chapters       *rssPodcastLink `xml:"podcast:chapters,omitempty"`
}

func categories(extra map[string]any) []rssITunesTopic {
	list, _ := extra["itunes:category"].([]any)
	topics := make([]rssITunesTopic, 0, len(list))
	for _, value := range list {
		if name, ok := value.(string); ok {
			topics = append(topics, rssITunesTopic{Text: name})
		}
	}
	return topics
}

func podcastLink(extra map[string]any, key string) *rssPodcastLink {
	object, ok := extra[key].(map[string]any)
	if !ok {
		return nil
	}
	link := &rssPodcastLink{URL: text(object, "url"), Type: text(object, "type")}
	if link.URL == "" {
		return nil
	}
	return link
}

func renderRSS(f Feed, selfURL string) ([]byte, error) {
	channel := rssChannel{
		Title:          f.Title,
		Link:           f.HomeURL,
		Description:    f.Description,
		LastBuildDate:  f.Updated.UTC().Format(time.RFC1123Z),
		Generator:      f.Generator + " " + f.Version,
		Self:           rssAtomLink{Href: selfURL, Rel: "self", Type: "application/rss+xml"},
		ITunesAuthor:   f.Author,
		ITunesExplicit: "false",
		Categories:     categories(f.Extra),
	}
	if explicit := text(f.Extra, "itunes:explicit"); explicit != "" {
		channel.ITunesExplicit = explicit
	}
	if f.Image != "" {
		channel.Image = &rssImage{URL: f.Image, Title: f.Title, Link: f.HomeURL}
		channel.ITunesImage = &rssITunesImage{Href: f.Image}
	}
	for _, item := range f.Items {
		out := rssItem{
			Title:          item.Title,
			Link:           item.Link,
			GUID:           rssGUID{IsPermaLink: "false", Value: item.ID},
			PubDate:        item.Published.UTC().Format(time.RFC1123Z),
			Description:    item.ContentHTML,
			ITunesEpisode:  text(item.Extra, "itunes:episode"),
			ITunesSeason:   text(item.Extra, "itunes:season"),
			ITunesExplicit: text(item.Extra, "itunes:explicit"),
			Transcript:     podcastLink(item.Extra, "podcast:transcript"),
			Chapters:       podcastLink(item.Extra, "podcast:chapters"),
		}
		for _, e := range item.Enclosures {
			out.Enclosures = append(out.Enclosures, rssEnclosure{URL: e.URL, Length: e.Length, Type: e.Type})
		}
		if item.Duration > 0 {
			out.ITunesDuration = strconv.FormatInt(int64(item.Duration.Seconds()), 10)
		}
		if item.Image != "" {
			out.ITunesImage = &rssITunesImage{Href: item.Image}
		}
		channel.Items = append(channel.Items, out)
	}
	return marshalXML(rssFeed{
		Version: "2.0",
		Atom:    namespaceAtom,
		ITunes:  namespaceITunes,
		Podcast: namespacePodcast,
		Channel: channel,
	})
}
