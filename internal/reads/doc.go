// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package reads ties a read's stored history to the arithmetic in
// internal/schedule. It loads segments and entries, works out where a read has
// got to, and appends a segment for each adjustment.
package reads
