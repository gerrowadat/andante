// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package canon says when two sources are the same serial. It's the only thing that
// stops one being crawled twice, so it lives apart from the store and the API, which
// both need it.
package canon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// placeholder is what a numeric source's template counts with, which a URL escapes.
const placeholder = "{n}"

var defaultPorts = map[string]string{"http": "80", "https": "443"}

// URL normalises an address: lower-case scheme and host, no default port, no fragment,
// no trailing slash, and query parameters in order. An address that won't parse comes
// back trimmed, because it can still be compared with itself.
func URL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && port != defaultPorts[u.Scheme] {
		host += ":" + port
	}
	u.Host, u.Fragment, u.RawFragment = host, "", ""
	u.RawQuery = u.Query().Encode()
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	return strings.ReplaceAll(u.String(), "%7Bn%7D", placeholder)
}

// Key returns a source's canonical key, from its strategy and config.
func Key(strategy string, config []byte) (string, error) {
	var c struct {
		FeedURL     string `json:"feed_url"`
		StartURL    string `json:"start_url"`
		URLTemplate string `json:"url_template"`
	}
	if err := json.Unmarshal(config, &c); err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}
	for _, address := range []string{c.FeedURL, c.StartURL, c.URLTemplate} {
		if address != "" {
			return KeyOf(strategy, address), nil
		}
	}
	return "", errors.New("the config has no URL")
}

// KeyOf returns the canonical key of a strategy reading an address.
func KeyOf(strategy, address string) string {
	return strategy + " " + URL(address)
}
