// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"errors"
	"testing"
	"time"
)

const (
	dailyAt7  = "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0"
	weekdays3 = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7;BYMINUTE=0;BYSECOND=0"
)

var epoch = time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC) // A Monday.

// index is a test Index: seq 0 to len(dates)-1, all discovered at the epoch
// unless found says otherwise.
type index struct {
	dates []time.Time
	found map[int]time.Time
}

func (i index) Start() int { return 0 }
func (i index) End() int   { return len(i.dates) - 1 }

func (i index) OriginalDate(seq int) (time.Time, bool) {
	if seq < 0 || seq >= len(i.dates) || i.dates[seq].IsZero() {
		return time.Time{}, false
	}
	return i.dates[seq], true
}

func (i index) DiscoveredAt(seq int) time.Time {
	if at, ok := i.found[seq]; ok {
		return at
	}
	return epoch.Add(-time.Hour)
}

// weekly returns n entries a week apart.
func weekly(n int) index {
	dates := make([]time.Time, n)
	for i := range dates {
		dates[i] = time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7*i)
	}
	return index{dates: dates}
}

func daily(tz string) RRulePacing { return RRulePacing{RRule: dailyAt7, TimeZone: tz, PerRelease: 1} }

func mustReleased(t *testing.T, segs []Segment, at time.Time, idx Index) int {
	t.Helper()
	got, err := Released(segs, at, idx)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustApply(t *testing.T, segs []Segment, a Adjustment, at time.Time, idx Index) []Segment {
	t.Helper()
	s, err := Apply(segs, a, at, idx)
	if err != nil {
		t.Fatal(err)
	}
	return append(segs[:len(segs):len(segs)], s)
}

func TestRRuleReleases(t *testing.T) {
	idx := weekly(100)
	segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
	tests := []struct {
		at   time.Time
		want int
	}{
		{epoch, 0}, // The first entry is there immediately.
		{epoch.Add(18*time.Hour + time.Minute), 0}, // 06:01 on Tuesday.
		{epoch.Add(19 * time.Hour), 1},             // 07:00: inclusive.
		{epoch.AddDate(0, 0, 1).Add(19 * time.Hour), 2},
		{epoch.AddDate(0, 0, 9).Add(19 * time.Hour), 10},
	}
	for _, tt := range tests {
		if got := mustReleased(t, segs, tt.at, idx); got != tt.want {
			t.Errorf("Released(%s) = %d, want %d", tt.at, got, tt.want)
		}
	}
}

func TestPerRelease(t *testing.T) {
	idx := weekly(100)
	segs := []Segment{New(RRulePacing{RRule: weekdays3, TimeZone: "UTC", PerRelease: 3}, 0, 1, epoch)}
	// Tuesday 07:00, then Friday 07:00 after three more, then the weekend.
	if got := mustReleased(t, segs, epoch.AddDate(0, 0, 1), idx); got != 3 {
		t.Errorf("Tuesday: %d, want 3", got)
	}
	if got := mustReleased(t, segs, epoch.AddDate(0, 0, 4), idx); got != 12 {
		t.Errorf("Friday: %d, want 12", got)
	}
	if got := mustReleased(t, segs, epoch.AddDate(0, 0, 6), idx); got != 12 {
		t.Errorf("Sunday: %d, want 12", got)
	}
	first, err := ReleaseTime(segs, 1, idx)
	if err != nil {
		t.Fatal(err)
	}
	third, err := ReleaseTime(segs, 3, idx)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(third) {
		t.Errorf("one batch has two times: %s and %s", first, third)
	}
}

func TestReplay(t *testing.T) {
	idx := weekly(10)
	t.Run("speed 1 reproduces the original gaps", func(t *testing.T) {
		segs := []Segment{New(ReplayPacing{Speed: 1}, 0, 1, epoch)}
		for q := 1; q < 10; q++ {
			at, err := ReleaseTime(segs, q, idx)
			if err != nil {
				t.Fatal(err)
			}
			if want := epoch.Add(time.Duration(q) * 7 * day); !at.Equal(want) {
				t.Errorf("entry %d at %s, want %s", q, at, want)
			}
		}
	})
	t.Run("speed 7 turns weekly into daily", func(t *testing.T) {
		segs := []Segment{New(ReplayPacing{Speed: 7}, 0, 1, epoch)}
		if got := mustReleased(t, segs, epoch.Add(3*day), idx); got != 3 {
			t.Errorf("after 3 days: %d, want 3", got)
		}
	})
	t.Run("clamps apply per gap", func(t *testing.T) {
		segs := []Segment{New(ReplayPacing{Speed: 1, MinGap: 8 * day}, 0, 1, epoch)}
		if at, _ := ReleaseTime(segs, 2, idx); !at.Equal(epoch.Add(16 * day)) {
			t.Errorf("min_gap: %s", at)
		}
		segs = []Segment{New(ReplayPacing{Speed: 1, MaxGap: 2 * day}, 0, 1, epoch)}
		if at, _ := ReleaseTime(segs, 2, idx); !at.Equal(epoch.Add(4 * day)) {
			t.Errorf("max_gap: %s", at)
		}
	})
	t.Run("a hiatus is clamped", func(t *testing.T) {
		hiatus := weekly(3)
		hiatus.dates[2] = hiatus.dates[1].AddDate(2, 0, 0)
		segs := []Segment{New(ReplayPacing{Speed: 1, MaxGap: 30 * day}, 0, 1, epoch)}
		if at, _ := ReleaseTime(segs, 2, hiatus); !at.Equal(epoch.Add(37 * day)) {
			t.Errorf("hiatus: %s", at)
		}
	})
	t.Run("undated entries", func(t *testing.T) {
		undated := weekly(4)
		undated.dates[2] = time.Time{}
		segs := []Segment{New(ReplayPacing{Speed: 7, DefaultGap: 5 * day}, 0, 1, epoch)}
		at, err := ReleaseTime(segs, 3, undated)
		if err != nil {
			t.Fatal(err)
		}
		if want := epoch.Add(11 * day); !at.Equal(want) {
			t.Errorf("entry 3 at %s, want %s: one day, then two default gaps", at, want)
		}
		segs = []Segment{New(ReplayPacing{Speed: 7, MinGap: 3 * day}, 0, 1, epoch)}
		if at, _ := ReleaseTime(segs, 2, undated); !at.Equal(epoch.Add(6 * day)) {
			t.Errorf("max(min_gap, P1D): %s", at)
		}
		segs = []Segment{New(ReplayPacing{Speed: 7}, 0, 1, epoch)}
		if at, _ := ReleaseTime(segs, 2, undated); !at.Equal(epoch.Add(2 * day)) {
			t.Errorf("P1D: %s", at)
		}
	})
	t.Run("dates that run backwards have no gap", func(t *testing.T) {
		odd := weekly(3)
		odd.dates[1], odd.dates[2] = odd.dates[2], odd.dates[1]
		segs := []Segment{New(ReplayPacing{Speed: 1}, 0, 1, epoch)}
		if at, _ := ReleaseTime(segs, 2, odd); !at.Equal(epoch.Add(14 * day)) {
			t.Errorf("entry 2 at %s", at)
		}
	})
}

func TestDST(t *testing.T) {
	const at1 = "FREQ=DAILY;BYHOUR=1;BYMINUTE=30;BYSECOND=0"
	tests := []struct {
		name, tz, rule string
		start          time.Time
		seq            int
		want           string
	}{
		{"Dublin stays at 07:00 into summer", "Europe/Dublin", dailyAt7,
			time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC), 3, "2026-03-30T07:00:00+01:00"},
		{"Dublin stays at 07:00 into winter", "Europe/Dublin", dailyAt7,
			time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), 3, "2026-10-26T07:00:00Z"},
		{"New York stays at 07:00 into summer", "America/New_York", dailyAt7,
			time.Date(2026, 3, 6, 17, 0, 0, 0, time.UTC), 3, "2026-03-09T07:00:00-04:00"},
		{"New York stays at 07:00 into winter", "America/New_York", dailyAt7,
			time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), 3, "2026-11-02T07:00:00-05:00"},
		// 01:30 does not exist in Dublin on 29 March 2026, and Go, which the
		// library builds on, moves it an hour on.
		{"Dublin's non-existent 01:30", "Europe/Dublin", at1,
			time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC), 2, "2026-03-29T02:30:00+01:00"},
		{"the day after it is back at 01:30", "Europe/Dublin", at1,
			time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC), 3, "2026-03-30T01:30:00+01:00"},
		// 01:30 happens twice as the clocks go back. It occurs once, at the
		// second one, which is the one Go's time.Date picks here.
		{"Dublin's repeated 01:30", "Europe/Dublin", at1,
			time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), 2, "2026-10-25T01:30:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := weekly(50)
			segs := []Segment{New(RRulePacing{RRule: tt.rule, TimeZone: tt.tz, PerRelease: 1}, 0, 1, tt.start)}
			at, err := ReleaseTime(segs, tt.seq, idx)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339, tt.want)
			if err != nil {
				t.Fatal(err)
			}
			if !at.Equal(want) {
				t.Errorf("released at %s, want %s", at, want)
			}
		})
	}
}

func TestEndOfIndex(t *testing.T) {
	t.Run("passthrough", func(t *testing.T) {
		idx := weekly(3)
		idx.found = map[int]time.Time{2: epoch.AddDate(0, 0, 10)}
		segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
		if got := mustReleased(t, segs, epoch.AddDate(0, 0, 5), idx); got != 1 {
			t.Errorf("before discovery: %d, want the index's 1", got)
		}
		at, err := ReleaseTime(segs, 2, idx)
		if err != nil {
			t.Fatal(err)
		}
		if !at.Equal(idx.found[2]) {
			t.Errorf("released at %s, want its discovery at %s", at, idx.found[2])
		}
		due, err := Due(segs, epoch.AddDate(0, 0, 5), idx)
		if err != nil {
			t.Fatal(err)
		}
		if due != 5 {
			t.Errorf("Due = %d, want 5, past the index", due)
		}
	})
	t.Run("buffer holds entries back", func(t *testing.T) {
		idx := weekly(6)
		idx.found = map[int]time.Time{5: epoch.AddDate(0, 0, 30)}
		segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
		buffered := Buffered(idx, 2)
		if got := mustReleased(t, segs, epoch.AddDate(0, 0, 20), buffered); got != 2 {
			t.Errorf("Released = %d, want 2: seq 4 known, less 2", got)
		}
		at, err := ReleaseTime(segs, 3, buffered)
		if err != nil {
			t.Fatal(err)
		}
		if !at.Equal(idx.found[5]) {
			t.Errorf("seq 3 released at %s, want the discovery of seq 5", at)
		}
		if got := mustReleased(t, segs, idx.found[5], buffered); got != 3 {
			t.Errorf("after seq 5 was found: %d, want 3", got)
		}
	})
	t.Run("no buffer is the index itself", func(t *testing.T) {
		idx := weekly(1)
		if _, ok := Buffered(idx, 0).(index); !ok {
			t.Error("Buffered(0) wrapped the index")
		}
	})
	t.Run("an empty index", func(t *testing.T) {
		idx := index{}
		segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
		if got := mustReleased(t, segs, epoch.AddDate(0, 0, 1), idx); got != -1 {
			t.Errorf("Released = %d, want -1", got)
		}
		if _, ok, err := NextRelease(segs, epoch, idx); ok || err != nil {
			t.Errorf("NextRelease: %v, %v", ok, err)
		}
	})
}

func TestNextRelease(t *testing.T) {
	idx := weekly(3)
	segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
	next, ok, err := NextRelease(segs, epoch, idx)
	if err != nil || !ok {
		t.Fatalf("NextRelease: %v, %v", ok, err)
	}
	if want := epoch.Add(19 * time.Hour); !next.Equal(want) {
		t.Errorf("next = %s, want %s", next, want)
	}
	if _, ok, _ := NextRelease(segs, epoch.AddDate(1, 0, 0), idx); ok {
		t.Error("a next release past the index")
	}
	paused := mustApply(t, segs, Pause{}, epoch.Add(time.Hour), idx)
	if _, ok, _ := NextRelease(paused, epoch.Add(2*time.Hour), idx); ok {
		t.Error("a next release while paused")
	}
}

func TestErrors(t *testing.T) {
	idx := weekly(3)
	if _, err := Released(nil, epoch, idx); !errors.Is(err, ErrNoSegments) {
		t.Errorf("no segments: %v", err)
	}
	if _, err := ReleaseTime(nil, 0, idx); !errors.Is(err, ErrNoSegments) {
		t.Errorf("no segments: %v", err)
	}
	if _, err := Apply(nil, Pause{}, epoch, idx); !errors.Is(err, ErrNoSegments) {
		t.Errorf("no segments: %v", err)
	}
	segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
	if _, err := Released(segs, epoch.Add(-time.Hour), idx); !errors.Is(err, ErrNotStarted) {
		t.Errorf("before the start: %v", err)
	}
	if _, err := ReleaseTime(segs, 99, idx); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("past the index: %v", err)
	}
	broken := []Segment{{FromSeq: 0, CreatedAt: epoch, StartsAt: epoch}}
	if _, err := Released(broken, epoch, idx); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("no pacing: %v", err)
	}
	if _, err := ReleaseTime(broken, 1, idx); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("no pacing: %v", err)
	}
	bad := []Segment{New(RRulePacing{RRule: "nope", TimeZone: "UTC", PerRelease: 1}, 0, 1, epoch)}
	if _, err := Released(bad, epoch, idx); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("bad rule: %v", err)
	}
	if _, err := ReleaseTime(bad, 1, idx); !errors.Is(err, ErrInvalidPacing) {
		t.Errorf("bad rule: %v", err)
	}
}

func TestValidatePacing(t *testing.T) {
	valid := []Pacing{
		daily("Europe/Dublin"),
		RRulePacing{RRule: "FREQ=HOURLY", TimeZone: "UTC", PerRelease: 2},
		ReplayPacing{Speed: 0.5, MinGap: day, MaxGap: 2 * day},
	}
	for _, p := range valid {
		if err := ValidatePacing(p); err != nil {
			t.Errorf("ValidatePacing(%+v) = %v", p, err)
		}
	}
	invalid := map[string]Pacing{
		"bad rule":             RRulePacing{RRule: "FREQ=NEVER", TimeZone: "UTC", PerRelease: 1},
		"empty rule":           RRulePacing{RRule: "", TimeZone: "UTC", PerRelease: 1},
		"DTSTART":              RRulePacing{RRule: "DTSTART:20200101T000000Z\nRRULE:FREQ=DAILY", TimeZone: "UTC", PerRelease: 1},
		"bad zone":             RRulePacing{RRule: dailyAt7, TimeZone: "Mars/Olympus", PerRelease: 1},
		"parses, cannot build": RRulePacing{RRule: "FREQ=DAILY;BYHOUR=99", TimeZone: "UTC", PerRelease: 1},
		"zero per":             RRulePacing{RRule: dailyAt7, TimeZone: "UTC"},
		"minutely":             RRulePacing{RRule: "FREQ=MINUTELY", TimeZone: "UTC", PerRelease: 1},
		"secondly":             RRulePacing{RRule: "FREQ=SECONDLY", TimeZone: "UTC", PerRelease: 1},
		"zero speed":           ReplayPacing{},
		"negative gap":         ReplayPacing{Speed: 1, MinGap: -1},
		"min over max":         ReplayPacing{Speed: 1, MinGap: 2, MaxGap: 1},
		"paused":               Paused{},
		"nil":                  nil,
	}
	for name, p := range invalid {
		if err := ValidatePacing(p); !errors.Is(err, ErrInvalidPacing) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
