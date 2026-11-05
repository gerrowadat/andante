// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package schedule computes what a read has released from its segments, an
// index view and a time, for both RRULE and replay pacing. It is pure: it
// does no I/O and never reads the clock.
package schedule
