// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownStrategy means no registered strategy has the given name.
var ErrUnknownStrategy = errors.New("unknown strategy")

// Strategy is one way of discovering a source's archive. Phase 5 adds crawling
// to it.
type Strategy interface {
	// Name is the value stored in sources.strategy.
	Name() string
	// Validate decodes and checks a source's config. Called on write, so that
	// a bad config fails when it is set rather than at crawl time.
	Validate(raw json.RawMessage) error
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

// Validate checks a config against the named strategy.
func (r *Registry) Validate(name string, raw json.RawMessage) error {
	s, ok := r.strategies[name]
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownStrategy, name)
	}
	return s.Validate(raw)
}
