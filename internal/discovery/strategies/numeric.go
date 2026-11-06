// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/extract"
)

const (
	placeholder   = "{n}"
	defaultMisses = 5
	formatHTML    = "html"
	formatJSON    = "json"
)

// Numeric walks URLs that carry an incrementing ID.
type Numeric struct{}

type jsonFields struct {
	Title   string `json:"title"`
	Image   string `json:"image"`
	AltText string `json:"alt_text"`
	Date    string `json:"date"`
	Year    string `json:"year"`
	Month   string `json:"month"`
	Day     string `json:"day"`
}

type numericConfig struct {
	Strategy             string     `json:"strategy"`
	URLTemplate          string     `json:"url_template"`
	Start                int64      `json:"start"`
	MaxConsecutiveMisses int        `json:"max_consecutive_misses"`
	Format               string     `json:"format"`
	Fields               jsonFields `json:"fields"`
}

// numericCursor is the next ID to try. Started tells a cursor at ID 0 from none.
type numericCursor struct {
	Started bool  `json:"started"`
	Next    int64 `json:"next"`
}

// Name implements discovery.Strategy.
func (Numeric) Name() string { return "numeric" }

func decodeNumericConfig(raw json.RawMessage) (numericConfig, error) {
	var c numericConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return numericConfig{}, fmt.Errorf("decode numeric config: %w", err)
	}
	if !strings.Contains(c.URLTemplate, placeholder) {
		return numericConfig{}, errors.New("url_template must contain {n}")
	}
	u, err := url.Parse(strings.ReplaceAll(c.URLTemplate, placeholder, "1"))
	if err != nil {
		return numericConfig{}, fmt.Errorf("url_template: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return numericConfig{}, errors.New("url_template must be an absolute http(s) URL")
	}
	if c.Format != "" && c.Format != formatHTML && c.Format != formatJSON {
		return numericConfig{}, errors.New("format must be html or json")
	}
	if c.Format == formatJSON && c.Fields.Title == "" && c.Fields.Image == "" {
		return numericConfig{}, errors.New("format json needs fields, at least a title or an image")
	}
	if c.MaxConsecutiveMisses <= 0 {
		c.MaxConsecutiveMisses = defaultMisses
	}
	return c, nil
}

// Validate implements discovery.Strategy.
func (Numeric) Validate(raw json.RawMessage) error {
	_, err := decodeNumericConfig(raw)
	return err
}

// Next implements discovery.Strategy. A 404 inside the archive is a gap, which
// takes no seq. A run of them is the live end.
func (Numeric) Next(ctx context.Context, fetcher discovery.PageFetcher, src discovery.Source, cur discovery.Cursor, limit int) (discovery.Batch, error) {
	config, err := decodeNumericConfig(src.Config)
	if err != nil {
		return discovery.Batch{}, err
	}
	var cursor numericCursor
	if len(cur) > 0 {
		if err := json.Unmarshal(cur, &cursor); err != nil {
			return discovery.Batch{}, fmt.Errorf("decode numeric cursor: %w", err)
		}
	}
	if !cursor.Started {
		cursor = numericCursor{Started: true, Next: config.Start}
	}

	var batch discovery.Batch
	misses := 0
	id := cursor.Next
	for len(batch.Entries) < limit && misses < config.MaxConsecutiveMisses {
		target := strings.ReplaceAll(config.URLTemplate, placeholder, strconv.FormatInt(id, 10))
		id++
		resp, err := fetcher.Fetch(ctx, target)
		var status *discovery.StatusError
		if errors.As(err, &status) && status.Status == http.StatusNotFound {
			misses++
			continue
		}
		if err != nil {
			if len(batch.Entries) > 0 {
				break
			}
			return discovery.Batch{}, err
		}
		entry, warnings, err := numericEntry(ctx, fetcher, config, src.DefaultKind, resp)
		if err != nil {
			return discovery.Batch{}, err
		}
		batch.Entries = append(batch.Entries, entry)
		batch.Warnings = append(batch.Warnings, warnings...)
		misses = 0
		cursor.Next = id
	}
	batch.LiveEnd = misses >= config.MaxConsecutiveMisses
	if batch.LiveEnd {
		batch.Warnings = append(batch.Warnings, fmt.Sprintf("%d misses in a row after ID %d; treating that as the live end", misses, cursor.Next-1))
	}
	if batch.Cursor, err = json.Marshal(cursor); err != nil {
		return discovery.Batch{}, fmt.Errorf("encode numeric cursor: %w", err)
	}
	return batch, nil
}

func numericEntry(ctx context.Context, fetcher discovery.PageFetcher, config numericConfig, kind string, resp *discovery.Response) (discovery.Entry, []string, error) {
	if config.Format == formatJSON {
		e, err := jsonEntry(config.Fields, kind, resp)
		return e, nil, err
	}
	extractor, err := extract.For(kind)
	if err != nil {
		return discovery.Entry{}, nil, err
	}
	result, err := extractor.Extract(extract.Page{URL: resp.URL, Body: resp.Body}, extract.Overrides{})
	if err != nil {
		return discovery.Entry{}, nil, fmt.Errorf("extract %s: %w", resp.URL, err)
	}
	f := result.Fields
	return discovery.Entry{
		Kind: kind, URL: resp.URL, Title: f.Title, Summary: f.Summary, Images: f.Images, AltText: f.AltText,
		Enclosures: enclosuresOf(ctx, fetcher, f.Enclosures), Date: f.Date,
	}, prefixed(resp.URL, result.Warnings), nil
}

// lookup follows a dotted path, and renders what it finds as text.
func lookup(document map[string]any, path string) string {
	if path == "" {
		return ""
	}
	var current any = document
	for key := range strings.SplitSeq(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	switch value := current.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	return ""
}

func jsonEntry(fields jsonFields, kind string, resp *discovery.Response) (discovery.Entry, error) {
	var document map[string]any
	if err := json.Unmarshal(resp.Body, &document); err != nil {
		return discovery.Entry{}, fmt.Errorf("%s is not a JSON object: %w", resp.URL, err)
	}
	e := discovery.Entry{Kind: kind, URL: resp.URL, Title: lookup(document, fields.Title), AltText: lookup(document, fields.AltText)}
	if image := lookup(document, fields.Image); image != "" {
		e.Images = []string{image}
	}
	e.Date = extract.ParseDate(lookup(document, fields.Date))
	if e.Date == nil {
		e.Date = dateOf(lookup(document, fields.Year), lookup(document, fields.Month), lookup(document, fields.Day))
	}
	return e, nil
}

func dateOf(year, month, day string) *time.Time {
	y, errY := strconv.Atoi(year)
	m, errM := strconv.Atoi(month)
	d, errD := strconv.Atoi(day)
	if errY != nil || errM != nil || errD != nil {
		return nil
	}
	date := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return &date
}
