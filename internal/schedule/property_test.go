// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"testing"
	"time"

	"github.com/teambition/rrule-go"
	"pgregory.net/rapid"
)

// Run with a fixed seed as well as a random one, as CI does: go test -rapid.seed=1.
// rapid prints the seed of any failure.

func genPacing(t *rapid.T) Pacing {
	if rapid.Bool().Draw(t, "replay") {
		return ReplayPacing{
			Speed:      rapid.Float64Range(0.5, 30).Draw(t, "speed"),
			MinGap:     time.Duration(rapid.IntRange(0, 2).Draw(t, "min")) * 6 * time.Hour,
			MaxGap:     time.Duration(rapid.IntRange(0, 5).Draw(t, "max")) * day * 2,
			DefaultGap: time.Duration(rapid.IntRange(0, 3).Draw(t, "default")) * day,
		}
	}
	rule := rapid.SampledFrom([]string{
		dailyAt7, weekdays3, "FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=8;BYMINUTE=0;BYSECOND=0",
		"FREQ=DAILY;INTERVAL=2;BYHOUR=1,13;BYMINUTE=30;BYSECOND=0",
	}).Draw(t, "rule")
	zone := rapid.SampledFrom([]string{"UTC", "Europe/Dublin", "America/New_York"}).Draw(t, "zone")
	return RRulePacing{RRule: rule, TimeZone: zone, PerRelease: rapid.IntRange(1, 3).Draw(t, "per")}
}

func genIndex(t *rapid.T) index {
	n := rapid.IntRange(30, 80).Draw(t, "entries")
	dates := make([]time.Time, n)
	at := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range dates {
		at = at.Add(time.Duration(rapid.IntRange(0, 20).Draw(t, "gap")) * day)
		if !rapid.Bool().Draw(t, "undated") || i%3 != 0 {
			dates[i] = at
		}
	}
	return index{dates: dates}
}

// genSegments builds a read's history by applying random adjustments.
func genSegments(t *rapid.T, idx index) ([]Segment, time.Time) {
	pacing := genPacing(t)
	segs := []Segment{New(pacing, 0, rapid.IntRange(0, 2).Draw(t, "initial"), epoch)}
	now := epoch
	for range rapid.IntRange(0, 6).Draw(t, "adjustments") {
		now = now.Add(time.Duration(rapid.IntRange(0, 96).Draw(t, "hours")) * time.Hour)
		var a Adjustment
		switch rapid.IntRange(0, 4).Draw(t, "kind") {
		case 0:
			a = Skip{Count: rapid.IntRange(1, 3).Draw(t, "skip")}
		case 1:
			a = Pause{}
		case 2:
			a = Resume{}
		case 3:
			a = Jump{Seq: rapid.IntRange(0, idx.End()).Draw(t, "jump")}
		default:
			a = Repace{genPacing(t)}
		}
		if s, err := Apply(segs, a, now, idx); err == nil {
			segs = append(segs, s)
		}
	}
	return segs, now
}

func TestReleasedIsMonotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		idx := genIndex(t)
		segs, last := genSegments(t, idx)
		previous := -1 << 31
		for at := epoch; at.Before(last.Add(30 * day)); at = at.Add(rapid.SampledFrom([]time.Duration{time.Hour, 7 * time.Hour, 3 * day}).Draw(t, "step")) {
			released, err := Released(segs, at, idx)
			if err != nil {
				t.Fatal(err)
			}
			if released < previous {
				t.Fatalf("released went from %d to %d at %s", previous, released, at)
			}
			previous = released
		}
	})
}

func TestReleaseTimeAgreesWithReleased(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		idx := genIndex(t)
		segs, last := genSegments(t, idx)
		at := epoch.Add(time.Duration(rapid.Int64Range(0, int64(last.Add(60*day).Sub(epoch))).Draw(t, "t")))
		released, err := Released(segs, at, idx)
		if err != nil {
			t.Fatal(err)
		}
		for q := 0; q <= idx.End(); q++ {
			when, err := ReleaseTime(segs, q, idx)
			if err != nil {
				if q <= released {
					t.Fatalf("entry %d is released but has no time: %v", q, err)
				}
				continue
			}
			if got, want := !when.After(at), q <= released; got != want {
				t.Fatalf("at %s, entry %d: ReleaseTime %s, Released %d", at, q, when, released)
			}
		}
	})
}

func TestLaterSegmentsNeverChangeReleaseTimes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		idx := genIndex(t)
		segs, now := genSegments(t, idx)
		now = now.Add(time.Duration(rapid.IntRange(1, 200).Draw(t, "wait")) * time.Hour)
		released, err := Released(segs, now, idx)
		if err != nil {
			t.Fatal(err)
		}
		before := map[int]time.Time{}
		for q := 0; q <= released; q++ {
			when, err := ReleaseTime(segs, q, idx)
			if err != nil {
				t.Fatal(err)
			}
			before[q] = when
		}

		a := rapid.SampledFrom([]Adjustment{Pause{}, Resume{}, Skip{Count: 1}, Repace{daily("UTC")}, Repace{ReplayPacing{Speed: 3}}}).Draw(t, "next")
		s, err := Apply(segs, a, now, idx)
		if err != nil {
			t.Skip("that adjustment does not apply here")
		}
		after := append(segs[:len(segs):len(segs)], s)
		for q, want := range before {
			got, err := ReleaseTime(after, q, idx)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) {
				t.Fatalf("entry %d moved from %s to %s", q, want, got)
			}
		}
	})
}

// quiet builds a read that has not outrun its index, which is where the two
// "changes nothing" properties hold.
func quiet(t *rapid.T) (index, []Segment, time.Time) {
	idx := genIndex(t)
	pacing := genPacing(t)
	segs := []Segment{New(pacing, 0, 1, epoch)}
	return idx, segs, epoch.Add(time.Duration(rapid.IntRange(0, 500).Draw(t, "hours")) * time.Hour)
}

func releaseTimes(t *rapid.T, segs []Segment, idx index) map[int]time.Time {
	times := map[int]time.Time{}
	for q := 0; q <= idx.End(); q++ {
		when, err := ReleaseTime(segs, q, idx)
		if err != nil {
			t.Fatal(err)
		}
		times[q] = when
	}
	return times
}

func sameTimes(t *rapid.T, a, b map[int]time.Time) {
	for q, want := range a {
		if !want.Equal(b[q]) {
			t.Fatalf("entry %d moved from %s to %s", q, want, b[q])
		}
	}
}

func outrun(segs []Segment, now time.Time, idx index) bool {
	due, _ := Due(segs, now, idx)
	return due > idx.End()
}

func TestANoOpSegmentChangesNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		idx, segs, now := quiet(t)
		if outrun(segs, now, idx) {
			t.Skip("the read has outrun its index")
		}
		s, err := Apply(segs, Repace{segs[0].Pacing}, now, idx)
		if err != nil {
			t.Fatal(err)
		}
		sameTimes(t, releaseTimes(t, segs, idx), releaseTimes(t, append(segs[:1:1], s), idx))
	})
}

func TestPauseThenResumeChangesNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		idx, segs, now := quiet(t)
		if outrun(segs, now, idx) {
			t.Skip("the read has outrun its index")
		}
		paused, err := Apply(segs, Pause{}, now, idx)
		if err != nil {
			t.Fatal(err)
		}
		after := append(segs[:1:1], paused)
		resumed, err := Apply(after, Resume{}, now, idx)
		if err != nil {
			t.Fatal(err)
		}
		sameTimes(t, releaseTimes(t, segs, idx), releaseTimes(t, append(after, resumed), idx))
	})
}

// bruteCount is what the closed form has to match.
func bruteCount(t *rapid.T, p RRulePacing, start, to time.Time) int {
	option, _, err := parse(p, start)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(p.TimeZone)
	option.Dtstart = start.In(loc)
	parsed, err := rrule.NewRRule(option)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	next := parsed.Iterator()
	for {
		at, ok := next()
		if !ok || at.After(to) {
			return count
		}
		if at.After(start) {
			count++
		}
	}
}

func TestCountingMatchesWalkingEveryOccurrence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := RRulePacing{
			RRule: rapid.SampledFrom([]string{
				dailyAt7, weekdays3, "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,SA;BYHOUR=1;BYMINUTE=30;BYSECOND=0",
				"FREQ=DAILY;INTERVAL=3;BYHOUR=2;BYMINUTE=30;BYSECOND=0", "FREQ=DAILY;BYDAY=MO,SU;BYHOUR=1,2,3;BYMINUTE=15;BYSECOND=0",
				"FREQ=WEEKLY", "FREQ=DAILY", "FREQ=DAILY;INTERVAL=2",
				"FREQ=MONTHLY;BYMONTHDAY=1;BYHOUR=9;BYMINUTE=0;BYSECOND=0", "FREQ=DAILY;COUNT=40", "FREQ=HOURLY;INTERVAL=7",
			}).Draw(t, "rule"),
			TimeZone:   rapid.SampledFrom([]string{"UTC", "Europe/Dublin", "America/New_York", "Australia/Lord_Howe", "America/Havana"}).Draw(t, "zone"),
			PerRelease: 1,
		}
		start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rapid.Int64Range(0, int64(800*day)).Draw(t, "start")))
		start = start.Truncate(time.Second)
		to := start.Add(time.Duration(rapid.Int64Range(0, int64(900*day)).Draw(t, "span")))

		compiled, err := compile(p, start)
		if err != nil {
			t.Fatal(err)
		}
		got, err := compiled.count(to)
		if err != nil {
			t.Fatal(err)
		}
		if want := bruteCount(t, p, start, to); got != want {
			t.Fatalf("count = %d, want %d", got, want)
		}

		if got > 0 {
			n := rapid.IntRange(1, got).Draw(t, "n")
			at, ok, err := compiled.nth(n)
			if err != nil || !ok {
				t.Fatalf("nth(%d): %v, %v", n, ok, err)
			}
			if want := bruteCount(t, p, start, at); want != n {
				t.Fatalf("nth(%d) = %s, which is occurrence %d", n, at, want)
			}
		}
	})
}
