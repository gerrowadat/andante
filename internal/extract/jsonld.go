// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/piprate/json-gold/ld"
)

const schemaOrg = "https://schema.org/"

// creativeWorks are the schema.org types an entry can be described by, most
// specific first.
var creativeWorks = []string{
	"ComicStory", "ComicIssue", "Article", "BlogPosting", "NewsArticle", "PodcastEpisode", "CreativeWork",
}

// offlineLoader serves schema.org's context from here and refuses anything else.
// A JSON-LD processor would otherwise fetch contexts over the network, and a
// page must not be able to make andante request an arbitrary URL.
type offlineLoader struct{}

func (offlineLoader) LoadDocument(u string) (*ld.RemoteDocument, error) {
	normal := strings.TrimRight(strings.Replace(u, "http://", "https://", 1), "/")
	if normal != strings.TrimRight(schemaOrg, "/") && !strings.HasPrefix(normal, schemaOrg) {
		return nil, ld.NewJsonLdError(ld.LoadingRemoteContextFailed, "remote context refused: "+u)
	}
	context := map[string]any{"@context": map[string]any{"@vocab": schemaOrg}}
	return &ld.RemoteDocument{DocumentURL: u, Document: context}, nil
}

// jsonLD returns what the page's JSON-LD says about the work on it. A block that
// cannot be read is a warning and not a failure, because the rest of the page
// may still have what is needed.
func jsonLD(doc *goquery.Document) (Fields, []string) {
	var (
		fields   Fields
		warnings []string
	)
	processor := ld.NewJsonLdProcessor()
	options := ld.NewJsonLdOptions("")
	options.DocumentLoader = offlineLoader{}

	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		var raw any
		if err := json.Unmarshal([]byte(s.Text()), &raw); err != nil {
			warnings = append(warnings, "a JSON-LD block is not valid JSON")
			return
		}
		expanded, err := processor.Expand(raw, options)
		if err != nil {
			warnings = append(warnings, "a JSON-LD block could not be processed")
			return
		}
		for _, node := range nodes(expanded) {
			if !isCreativeWork(node) {
				continue
			}
			merge(&fields, fieldsOf(node))
		}
	})
	return fields, warnings
}

// nodes flattens expanded JSON-LD into its objects, looking inside @graph.
func nodes(expanded []any) []map[string]any {
	var out []map[string]any
	for _, item := range expanded {
		node, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, node)
		if graph, ok := node["@graph"].([]any); ok {
			out = append(out, nodes(graph)...)
		}
	}
	return out
}

func isCreativeWork(node map[string]any) bool {
	types, _ := node["@type"].([]any)
	for _, t := range types {
		name, _ := t.(string)
		for _, work := range creativeWorks {
			if name == schemaOrg+work {
				return true
			}
		}
	}
	return false
}

func fieldsOf(node map[string]any) Fields {
	f := Fields{Title: first(node, "name")}
	if f.Title == "" {
		f.Title = first(node, "headline")
	}
	for _, key := range []string{"image", "associatedMedia"} {
		if image := reference(node, key); image != "" {
			f.Images = []string{image}
			break
		}
	}
	f.media = reference(node, "associatedMedia")
	f.Summary = first(node, "description")
	if value := first(node, "datePublished"); value != "" {
		f.Date = parseDate(value)
	}
	return f
}

// first returns the first plain value of a schema.org property.
func first(node map[string]any, key string) string {
	values, _ := node[schemaOrg+key].([]any)
	for _, v := range values {
		object, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := object["@value"].(string); ok {
			return text
		}
	}
	return ""
}

// reference returns a property that may be a plain URL, an @id, or an object
// such as an ImageObject with a url or contentUrl.
func reference(node map[string]any, key string) string {
	if value := first(node, key); value != "" {
		return value
	}
	values, _ := node[schemaOrg+key].([]any)
	for _, v := range values {
		object, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for _, inner := range []string{"url", "contentUrl"} {
			if value := first(object, inner); value != "" {
				return value
			}
			if value := id(object, inner); value != "" {
				return value
			}
		}
		if value, ok := object["@id"].(string); ok {
			return value
		}
	}
	return ""
}

func id(node map[string]any, key string) string {
	values, _ := node[schemaOrg+key].([]any)
	for _, v := range values {
		if object, ok := v.(map[string]any); ok {
			if value, ok := object["@id"].(string); ok {
				return value
			}
		}
	}
	return ""
}

var dateLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"}

// ParseDate reads a date in the forms pages and feeds use. It returns nil for
// anything else.
func ParseDate(value string) *time.Time { return parseDate(value) }

func parseDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, value); err == nil {
			utc := t.UTC()
			return &utc
		}
	}
	return nil
}
