// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"testing"

	"github.com/gerrowadat/andante/internal/store/db"
)

// The website counts entries from 1, as its numbered lists do, so that a number
// read off the source page is the number the jump takes.
func TestTheWebsiteNumbersEntriesFromOne(t *testing.T) {
	if got := entryNumber(0); got != 1 {
		t.Errorf("seq 0 is entry %d, want 1", got)
	}
	if got := seqOf(1); got != 0 {
		t.Errorf("entry 1 is seq %d, want 0", got)
	}
	for _, seq := range []int32{0, 1, 41} {
		if got := seqOf(entryNumber(seq)); got != int(seq) {
			t.Errorf("seq %d comes back as %d", seq, got)
		}
	}
	if got := entryOf(db.Entry{Seq: 0}).Title; got != "Entry 1" {
		t.Errorf("the first entry, untitled, is %q", got)
	}
}
