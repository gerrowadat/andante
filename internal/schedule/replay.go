// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import "time"

// undatedGap is the gap for an undated entry when nothing else sets one.
const undatedGap = 24 * time.Hour

// gap returns how long after entry seq the entry seq+1 is released. A gap with an
// undated end has no original timing to speed up, so it stands as it is.
func (p ReplayPacing) gap(idx Index, seq int) time.Duration {
	from, fromOK := idx.OriginalDate(seq)
	to, toOK := idx.OriginalDate(seq + 1)
	if !fromOK || !toOK {
		if p.DefaultGap > 0 {
			return p.DefaultGap
		}
		return max(p.MinGap, undatedGap)
	}
	// A later entry dated earlier has no gap to keep.
	gap := time.Duration(float64(max(to.Sub(from), 0)) / p.Speed)
	if p.MinGap > 0 {
		gap = max(gap, p.MinGap)
	}
	if p.MaxGap > 0 {
		gap = min(gap, p.MaxGap)
	}
	return gap
}
