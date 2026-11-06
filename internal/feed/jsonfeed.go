// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/json"
	"fmt"
	"time"
)

const jsonFeedVersion = "https://jsonfeed.org/version/1.1"

type jsonFeed struct {
	Version     string       `json:"version"`
	Title       string       `json:"title"`
	HomePageURL string       `json:"home_page_url"`
	FeedURL     string       `json:"feed_url"`
	Description string       `json:"description"`
	Icon        string       `json:"icon,omitempty"`
	Authors     []jsonAuthor `json:"authors"`
	Items       []jsonItem   `json:"items"`
}

type jsonAuthor struct {
	Name string `json:"name"`
}

type jsonAttachment struct {
	URL      string `json:"url"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size_in_bytes,omitempty"`
	Duration int64  `json:"duration_in_seconds,omitempty"`
}

type jsonItem struct {
	ID            string           `json:"id"`
	URL           string           `json:"url,omitempty"`
	Title         string           `json:"title"`
	ContentHTML   string           `json:"content_html"`
	DatePublished string           `json:"date_published"`
	DateModified  string           `json:"date_modified"`
	Attachments   []jsonAttachment `json:"attachments,omitempty"`
}

func renderJSON(f Feed, selfURL string) ([]byte, error) {
	out := jsonFeed{
		Version:     jsonFeedVersion,
		Title:       f.Title,
		HomePageURL: f.HomeURL,
		FeedURL:     selfURL,
		Description: f.Description,
		Icon:        f.Image,
		Authors:     []jsonAuthor{{Name: f.Author}},
		Items:       make([]jsonItem, 0, len(f.Items)),
	}
	for _, item := range f.Items {
		when := item.Published.UTC().Format(time.RFC3339)
		converted := jsonItem{
			ID: item.ID, URL: item.Link, Title: item.Title, ContentHTML: item.ContentHTML,
			DatePublished: when, DateModified: when,
		}
		for _, e := range item.Enclosures {
			converted.Attachments = append(converted.Attachments, jsonAttachment{
				URL: e.URL, MIMEType: e.Type, Size: e.Length, Duration: int64(item.Duration.Seconds()),
			})
		}
		out.Items = append(out.Items, converted)
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode feed: %w", err)
	}
	return append(body, '\n'), nil
}
