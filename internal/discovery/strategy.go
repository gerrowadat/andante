// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Entry is one discovered item of a serial.
type Entry struct {
	Kind        string
	URL         string
	Title       string
	Summary     string
	Images      []string
	AltText     string
	Date        *time.Time
	ContentHash []byte
}

// Batch is the result of one call to Next.
type Batch struct {
	Entries  []Entry
	Updates  []Entry // Known pages whose metadata changed.
	Cursor   Cursor
	LiveEnd  bool // The strategy reached the end of the serial as it stands.
	Warnings []string
}

// PageFetcher is how a strategy requests pages. The crawl loop's adds conditional
// requests and logging, which a probe does not want.
type PageFetcher interface {
	Fetch(ctx context.Context, url string) (*Response, error)
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
