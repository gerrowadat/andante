// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// Crawl walks a source's pages by their next link.
type Crawl struct{}

type crawlConfig struct {
	Strategy  string `json:"strategy"`
	StartURL  string `json:"start_url"`
	Selectors struct {
		Title   string `json:"title"`
		Image   string `json:"image"`
		AltText string `json:"alt_text"`
		Date    string `json:"date"`
		Next    string `json:"next"`
	} `json:"selectors"`
}

// Name implements discovery.Strategy.
func (Crawl) Name() string { return "crawl" }

// Validate implements discovery.Strategy.
func (Crawl) Validate(raw json.RawMessage) error {
	var c crawlConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return fmt.Errorf("decode crawl config: %w", err)
	}
	u, err := url.Parse(c.StartURL)
	if err != nil {
		return fmt.Errorf("start_url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("start_url must be an absolute http(s) URL")
	}
	return nil
}
