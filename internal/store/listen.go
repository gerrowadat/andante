// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SourceChannel is the Postgres channel ApplyChanges announces a changed source on,
// with the schema and the source's ID as the payload. Every server sharing the
// database hears it, and only listens to its own schema's.
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
	var schema string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ready()
	for {
		notice, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		from, raw, _ := strings.Cut(notice.Payload, " ")
		if from != schema {
			continue // Another copy's, in the same database.
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		changed(id)
	}
}
