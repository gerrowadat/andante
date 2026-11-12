// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package throttle

import (
	"strconv"
	"testing"
	"time"
)

func TestTheWaitDoublesAndStops(t *testing.T) {
	th := New()
	now := time.Now()
	var last time.Duration
	for range freeForPair {
		th.Failed("a", "dave", now)
	}
	if got := th.Wait("a", "dave", now); got != 0 {
		t.Fatalf("wait during the free tries = %s", got)
	}
	for range 30 {
		th.Failed("a", "dave", now)
		got := th.Wait("a", "dave", now)
		if got < last || got > longestWait {
			t.Errorf("wait went from %s to %s", last, got)
		}
		last = got
	}
	if last != longestWait {
		t.Errorf("the wait stopped at %s", last)
	}
}

func TestSuccessClearsThePairAndNotTheAddress(t *testing.T) {
	th := New()
	now := time.Now()
	for range freeForAddress + 1 {
		th.Failed("a", "dave", now)
	}
	th.Succeeded("a", "dave")
	if got := th.Wait("a", "someone-else", now); got == 0 {
		t.Error("logging in cleared the address's strikes")
	}
}

func TestSweepingForgetsWhatsStale(t *testing.T) {
	th := New()
	now := time.Now()
	for i := range sweepAt {
		th.Failed("a"+strconv.Itoa(i), "dave", now)
	}
	later := now.Add(entryLife + time.Hour)
	th.Failed("fresh", "dave", later)
	if len(th.strikes) > 2 {
		t.Errorf("strikes left = %d", len(th.strikes))
	}
}
