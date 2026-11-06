// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fake struct{ err error }

func (fake) Name() string                     { return "fake" }
func (f fake) Validate(json.RawMessage) error { return f.err }
func (fake) Next(context.Context, PageFetcher, Source, Cursor, int) (Batch, error) {
	return Batch{}, nil
}

func TestRegistry(t *testing.T) {
	wantErr := errors.New("bad config")
	r := NewRegistry(fake{err: wantErr})
	if err := r.Validate("fake", nil); !errors.Is(err, wantErr) {
		t.Errorf("Validate(fake) = %v, want %v", err, wantErr)
	}
	if err := r.Validate("other", nil); !errors.Is(err, ErrUnknownStrategy) {
		t.Errorf("Validate(other) = %v, want ErrUnknownStrategy", err)
	}
}
