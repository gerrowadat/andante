// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package schedule computes what a read has released from its segments, an index
// view and a time, for both RRULE and replay pacing. It is pure: it does no I/O
// and never reads the clock.
//
// Nothing here is stored or counted up. A read's position is a function of its
// append-only segments and the time, so there is nothing to miss, double-run or
// repair after downtime, and an entry's release time comes out the same on every
// request.
//
// # Counting occurrences over long horizons
//
// A read created ten years ago at three a day has eleven thousand occurrences
// behind it, and walking them on every feed request is too slow. Daily and weekly
// rules repeat, so whole periods can be counted without walking them: over 7 ×
// INTERVAL days such a rule has the same number of occurrences, whatever the
// clocks did. Only the part of a period at either end is walked.
//
// The periods start at local noon. They cannot start at the segment's own time of
// day, because that time may not exist on a daylight saving day, and Go would move
// it, shifting an occurrence across the boundary. No time zone changes its clocks
// at noon.
//
// Other rules (monthly, yearly, or with COUNT or UNTIL) have far fewer occurrences
// and are walked, up to maxWalk. Rules more frequent than hourly are refused: they
// are not a human pace.
package schedule
