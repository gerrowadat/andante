// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import "github.com/jackc/pgx/v5/pgxpool"

// Pool exposes the connection pool to tests.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
