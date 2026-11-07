// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"strconv"
	"testing"
	"time"
)

func TestTheWaitDoublesAndStops(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	var last time.Duration
	for range freeForPair {
		th.failed("a", "dave", now)
	}
	if got := th.wait("a", "dave", now); got != 0 {
		t.Fatalf("wait during the free tries = %s", got)
	}
	for range 30 {
		th.failed("a", "dave", now)
		got := th.wait("a", "dave", now)
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
	th := newThrottle()
	now := time.Now()
	for range freeForAddress + 1 {
		th.failed("a", "dave", now)
	}
	th.succeeded("a", "dave")
	if got := th.wait("a", "someone-else", now); got == 0 {
		t.Error("logging in cleared the address's strikes")
	}
}

func TestSweepingForgetsWhatsStale(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	for i := range sweepAt {
		th.failed("a"+strconv.Itoa(i), "dave", now)
	}
	later := now.Add(entryLife + time.Hour)
	th.failed("fresh", "dave", later)
	if len(th.strikes) > 2 {
		t.Errorf("strikes left = %d", len(th.strikes))
	}
}
