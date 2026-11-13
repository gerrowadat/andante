// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ErrUnknownStrategy means no registered strategy has the given name.
var ErrUnknownStrategy = errors.New("unknown strategy")

// ErrCycle means a next chain came back to a page it had already been through.
var ErrCycle = errors.New("the archive's links form a cycle")

// Source is what a strategy needs to know about a serial.
type Source struct {
	ID          int64
	HomeURL     string
	DefaultKind string
	Config      json.RawMessage
}

// Cursor is a strategy's own resume state, kept in sources.cursor.
type Cursor = json.RawMessage

// Enclosure is a media file attached to an entry. It is linked, never fetched.
type Enclosure struct {
	URL    string `json:"url"`
	Type   string `json:"type,omitempty"`
	Length int64  `json:"length,omitempty"`
}

// Entry is one discovered item of a serial.
type Entry struct {
	Kind        string
	URL         string
	GUID        string
	Title       string
	Summary     string
	ContentHTML string
	Images      []string
	AltText     string
	Enclosures  []Enclosure
	Duration    time.Duration
	Extra       map[string]any
	Date        *time.Time
	ContentHash []byte
}

// SourceUpdate is show or site metadata a strategy found.
type SourceUpdate struct {
	ImageURL string
	Extra    map[string]any
}

// Batch is the result of one call to Next.
type Batch struct {
	Entries  []Entry
	Updates  []Entry // Known pages whose metadata changed.
	Source   *SourceUpdate
	Cursor   Cursor
	LiveEnd  bool // The strategy reached the end of the serial as it stands.
	Warnings []string
}

// Note is something a strategy decided or ran into. One about a page it gave
// up on, such as one with no next link, carries the page, so that someone can
// see what the crawl saw.
type Note struct {
	Level   slog.Level
	Message string
	URL     string
	Page    []byte
}

// PageFetcher is how a strategy requests pages, and says what it made of them.
type PageFetcher interface {
	// Fetch gets a page. It is never conditional: a strategy retrying after a
	// failed save needs the body, even if the page has not changed.
	Fetch(ctx context.Context, url string) (*Response, error)
	// Revalidate gets a page if it has changed since it was last fetched. An
	// unchanged page comes back with NotModified set and no body.
	Revalidate(ctx context.Context, url string) (*Response, error)
	// Head asks about a file without downloading it.
	Head(ctx context.Context, url string) (*Response, error)
	// Note records something the strategy decided or ran into, as it happens,
	// so that the crawl log has it in order with the requests.
	Note(ctx context.Context, note Note)
}

// Strategy is one way of discovering a source's archive.
type Strategy interface {
	// Name is the value stored in sources.strategy.
	Name() string
	// Validate decodes and checks a source's config. Called on write, so that
	// a bad config fails when it is set rather than at crawl time.
	Validate(raw json.RawMessage) error
	// Next discovers entries after the given cursor, up to limit. It returns
	// the entries found, the cursor to resume from, and whether the live end
	// has been reached.
	Next(ctx context.Context, fetcher PageFetcher, src Source, cur Cursor, limit int) (Batch, error)
}

// Registry holds the compiled-in strategies.
type Registry struct {
	strategies map[string]Strategy
}

// NewRegistry returns a registry of the given strategies.
func NewRegistry(strategies ...Strategy) *Registry {
	r := &Registry{strategies: make(map[string]Strategy, len(strategies))}
	for _, s := range strategies {
		r.strategies[s.Name()] = s
	}
	return r
}

// Get returns the named strategy.
func (r *Registry) Get(name string) (Strategy, error) {
	s, ok := r.strategies[name]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownStrategy, name)
	}
	return s, nil
}

// Validate checks a config against the named strategy.
func (r *Registry) Validate(name string, raw json.RawMessage) error {
	s, err := r.Get(name)
	if err != nil {
		return err
	}
	return s.Validate(raw)
}
