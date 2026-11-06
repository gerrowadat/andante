// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"errors"
	"testing"
	"time"
)

func TestLongHorizons(t *testing.T) {
	start := epoch.AddDate(-10, 0, 0)
	now := epoch
	idx := weekly(11000)
	segs := []Segment{New(RRulePacing{RRule: "FREQ=DAILY;BYHOUR=7,12,18;BYMINUTE=0;BYSECOND=0", TimeZone: "Europe/Dublin", PerRelease: 1}, 0, 1, start)}

	began := time.Now()
	released, err := Released(segs, now, idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReleaseTime(segs, released, idx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NextRelease(segs, now, idx); err != nil {
		t.Fatal(err)
	}
	// The race detector slows this a good deal, so this is far above what it
	// takes. Walking every occurrence is what it guards against.
	if took := time.Since(began); took > 50*time.Millisecond {
		t.Errorf("took %s", took)
	}
	if want := 3 * 3652; released < want-3 || released > want+3 {
		t.Errorf("released = %d, want about %d", released, want)
	}
}

func BenchmarkReleasedTenYears(b *testing.B) {
	start := epoch.AddDate(-10, 0, 0)
	idx := weekly(11000)
	segs := []Segment{New(RRulePacing{RRule: "FREQ=DAILY;BYHOUR=7,12,18;BYMINUTE=0;BYSECOND=0", TimeZone: "Europe/Dublin", PerRelease: 1}, 0, 1, start)}
	for b.Loop() {
		if _, err := Released(segs, epoch, idx); err != nil {
			b.Fatal(err)
		}
	}
}

func TestWalkingIsBounded(t *testing.T) {
	r, err := compile(RRulePacing{RRule: "FREQ=SECONDLY", TimeZone: "UTC", PerRelease: 1}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	far := epoch.AddDate(100, 0, 0)
	if _, err := r.count(far); !errors.Is(err, ErrTooManyOccurrences) {
		t.Errorf("count = %v", err)
	}
	if _, _, err := r.nth(maxWalk + 2); !errors.Is(err, ErrTooManyOccurrences) {
		t.Errorf("nth = %v", err)
	}
}

func TestFiniteRules(t *testing.T) {
	idx := weekly(10)
	segs := []Segment{New(RRulePacing{RRule: "FREQ=DAILY;COUNT=2", TimeZone: "UTC", PerRelease: 1}, 0, 1, epoch)}
	if _, err := ReleaseTime(segs, 5, idx); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("ReleaseTime = %v", err)
	}
	// COUNT includes the start itself (RFC 5545), and that releases nothing.
	if got := mustReleased(t, segs, epoch.AddDate(1, 0, 0), idx); got != 1 {
		t.Errorf("Released = %d, want 1", got)
	}
}

func TestARuleWithNoOccurrences(t *testing.T) {
	// INTERVAL is ignored for this check; what matters is an empty period.
	r, err := compile(RRulePacing{RRule: "FREQ=DAILY;BYHOUR=7", TimeZone: "UTC", PerRelease: 1}, epoch)
	if err != nil {
		t.Fatal(err)
	}
	r.perPeriod = 0
	if _, ok, err := r.nth(1); ok || err != nil {
		t.Errorf("nth = %v, %v", ok, err)
	}
}

func TestRepeats(t *testing.T) {
	tests := map[string]bool{
		dailyAt7:                            true,
		weekdays3:                           true,
		"FREQ=WEEKLY;INTERVAL=2":            true,
		"FREQ=MONTHLY":                      false,
		"FREQ=HOURLY":                       false,
		"FREQ=DAILY;COUNT=5":                false,
		"FREQ=DAILY;UNTIL=20300101T000000Z": false,
		"FREQ=DAILY;BYMONTH=1":              false,
		"FREQ=WEEKLY;BYMONTHDAY=1":          false,
		"FREQ=WEEKLY;BYDAY=MO;BYSETPOS=1":   false,
		"FREQ=DAILY;BYDAY=1MO":              false,
	}
	for text, want := range tests {
		option, _, err := parse(RRulePacing{RRule: text, TimeZone: "UTC", PerRelease: 1}, epoch)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if got := repeats(option); got != want {
			t.Errorf("repeats(%s) = %v, want %v", text, got, want)
		}
	}
}

func TestMarkers(t *testing.T) {
	RRulePacing{}.isPacing()
	ReplayPacing{}.isPacing()
	Paused{}.isPacing()
	Skip{}.isAdjustment()
	Pause{}.isAdjustment()
	Resume{}.isAdjustment()
	Jump{}.isAdjustment()
	Repace{}.isAdjustment()
}

func TestBrokenRulesSurface(t *testing.T) {
	idx := weekly(10)
	broken := RRulePacing{RRule: "nope", TimeZone: "UTC", PerRelease: 1}
	segs := []Segment{{CreatedAt: epoch, StartsAt: epoch, Pacing: broken}}
	for _, a := range []Adjustment{Skip{Count: 1}, Jump{Seq: 1}, Pause{}, Repace{broken}} {
		if _, err := Apply(segs, a, epoch, idx); !errors.Is(err, ErrInvalidPacing) {
			t.Errorf("Apply(%T) = %v", a, err)
		}
	}
	if _, err := startFor(segs, broken, epoch); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("startFor = %v", err)
	}
	if _, _, err := NextRelease(segs, epoch, idx); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("NextRelease = %v", err)
	}
	if _, _, err := NextRelease(nil, epoch, idx); !errors.Is(err, ErrNoSegments) {
		t.Errorf("NextRelease = %v", err)
	}
	if _, err := compile(RRulePacing{RRule: dailyAt7, TimeZone: "Mars/Olympus", PerRelease: 1}, epoch); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("compile = %v", err)
	}
}

func TestAnchorEdges(t *testing.T) {
	idx := weekly(3)
	segs := []Segment{New(daily("UTC"), 0, 0, epoch)}
	if got := anchor(segs, -1, idx); !got.Equal(epoch) {
		t.Errorf("before the index: %s", got)
	}
	if got := anchor(segs, 9, idx); !got.Equal(epoch) {
		t.Errorf("past the index: %s", got)
	}
	paused := []Segment{New(Paused{}, 0, 0, epoch)}
	if got := anchor(paused, 0, idx); !got.Equal(epoch) {
		t.Errorf("never scheduled: %s", got)
	}
}

func TestStartForAFiniteRuleThatHasEnded(t *testing.T) {
	once := RRulePacing{RRule: "FREQ=DAILY;COUNT=1", TimeZone: "UTC", PerRelease: 1}
	segs := []Segment{New(once, 0, 1, epoch)}
	got, err := startFor(segs, once, epoch.AddDate(0, 0, 5))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(epoch) {
		t.Errorf("start = %s, want the rule's own", got)
	}
}
