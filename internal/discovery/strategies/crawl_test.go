// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies

import (
	"encoding/json"
	"testing"
)

func TestCrawlValidate(t *testing.T) {
	tests := []struct {
		name, config string
		valid        bool
	}{
		{"minimal", `{"strategy":"crawl","start_url":"https://comic.example/1"}`, true},
		{"selectors", `{"strategy":"crawl","start_url":"http://comic.example/","selectors":{"next":"a.next"}}`, true},
		{"not JSON", `nope`, false},
		{"unknown field", `{"strategy":"crawl","start_url":"https://comic.example/","bogus":1}`, false},
		{"unknown selector", `{"strategy":"crawl","start_url":"https://comic.example/","selectors":{"bogus":"a"}}`, false},
		{"no start URL", `{"strategy":"crawl"}`, false},
		{"relative start URL", `{"strategy":"crawl","start_url":"/1"}`, false},
		{"non-http start URL", `{"strategy":"crawl","start_url":"file:///etc/passwd"}`, false},
		{"unparseable start URL", `{"strategy":"crawl","start_url":"http://[::1"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Crawl{}.Validate(json.RawMessage(tt.config))
			if (err == nil) != tt.valid {
				t.Errorf("Validate() = %v, want valid %v", err, tt.valid)
			}
		})
	}
}
