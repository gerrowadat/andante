// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package canon_test

import (
	"testing"

	"github.com/gerrowadat/andante/internal/canon"
)

func TestURL(t *testing.T) {
	const want = "https://comic.example/archive"
	for _, raw := range []string{
		"https://comic.example/archive",
		"https://comic.example/archive/",
		"HTTPS://Comic.Example/archive",
		"https://comic.example:443/archive",
		"https://comic.example/archive#latest",
		"  https://comic.example/archive  ",
	} {
		if got := canon.URL(raw); got != want {
			t.Errorf("URL(%q) = %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]string{
		"https://comic.example/":          "https://comic.example",
		"https://comic.example":           "https://comic.example",
		"http://comic.example:80/":        "http://comic.example",
		"http://comic.example:8080/":      "http://comic.example:8080",
		"https://comic.example/Archive":   "https://comic.example/Archive",
		"https://a.example/f?b=2&a=1":     "https://a.example/f?a=1&b=2",
		"https://a.example/{n}/info.json": "https://a.example/{n}/info.json",
		"https://a.example/p?id={n}":      "https://a.example/p?id={n}",
		"not a url":                       "not a url",
	} {
		if got := canon.URL(raw); got != want {
			t.Errorf("URL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestKey(t *testing.T) {
	for strategy, config := range map[string]string{
		"feed":    `{"strategy":"feed","feed_url":"https://A.example/rss/"}`,
		"crawl":   `{"strategy":"crawl","start_url":"https://a.example/rss"}`,
		"numeric": `{"strategy":"numeric","url_template":"https://a.example/rss","start":1}`,
	} {
		got, err := canon.Key(strategy, []byte(config))
		if err != nil {
			t.Fatal(err)
		}
		if want := strategy + " https://a.example/rss"; got != want {
			t.Errorf("Key = %q, want %q", got, want)
		}
	}
	if _, err := canon.Key("feed", []byte("{}")); err == nil {
		t.Error("a config with no URL got a key")
	}
	if _, err := canon.Key("feed", []byte("not json")); err == nil {
		t.Error("a broken config got a key")
	}
}
