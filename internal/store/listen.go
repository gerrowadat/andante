// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// SourceChannel is the Postgres channel ApplyChanges announces a changed source on,
// with its ID as the payload. Every server sharing the database hears it.
const SourceChannel = "andante_source"

// closeTimeout bounds saying goodbye to the database on a connection that may be
// broken.
const closeTimeout = 5 * time.Second

// ListenSources calls ready once it's listening, then changed with the ID of each
// source the sync changes, until ctx ends or the connection fails. It holds a
// connection of its own for as long as it runs.
func (s *Store) ListenSources(ctx context.Context, ready func(), changed func(sourceID int64)) error {
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// A connection that's listening can't go back to the pool, which would hand it
	// to someone else still listening.
	conn := pooled.Hijack()
	defer func() {
		closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = conn.Close(closing)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+SourceChannel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ready()
	for {
		notice, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		id, err := strconv.ParseInt(notice.Payload, 10, 64)
		if err != nil {
			continue
		}
		changed(id)
	}
}
