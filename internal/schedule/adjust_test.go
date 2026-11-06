// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"errors"
	"testing"
	"time"
)

func pacings() map[string]Pacing {
	return map[string]Pacing{"rrule": daily("UTC"), "replay": ReplayPacing{Speed: 7}}
}

func TestCreate(t *testing.T) {
	if s := New(daily("UTC"), 5, 1, epoch); s.FromSeq != 5 || s.Reason != ReasonCreated {
		t.Errorf("default: %+v", s)
	}
	if s := New(daily("UTC"), 5, 3, epoch); s.FromSeq != 7 {
		t.Errorf("three at once: %+v", s)
	}
	if s := New(daily("UTC"), 5, 0, epoch); s.FromSeq != 4 {
		t.Errorf("nothing at first: %+v", s)
	}
}

func TestActions(t *testing.T) {
	for name, pacing := range pacings() {
		t.Run(name, func(t *testing.T) {
			idx := weekly(50)
			segs := []Segment{New(pacing, 0, 1, epoch)}
			now := epoch.AddDate(0, 0, 3).Add(time.Hour)
			released := mustReleased(t, segs, now, idx)

			t.Run("skip", func(t *testing.T) {
				got, err := Apply(segs, Skip{Count: 4}, now, idx)
				if err != nil {
					t.Fatal(err)
				}
				if got.FromSeq != released+4 || !got.CreatedAt.Equal(now) || got.Reason != ReasonSkip {
					t.Errorf("%+v", got)
				}
				after := append(segs[:1:1], got)
				if at, _ := ReleaseTime(after, released+4, idx); !at.Equal(now) {
					t.Errorf("skipped entry released at %s, want now", at)
				}
			})
			t.Run("skip past the end stops there", func(t *testing.T) {
				got, err := Apply(segs, Skip{Count: 1000}, now, idx)
				if err != nil {
					t.Fatal(err)
				}
				if got.FromSeq != 49 {
					t.Errorf("from_seq = %d, want the end", got.FromSeq)
				}
			})
			t.Run("skip at the end", func(t *testing.T) {
				done := mustApply(t, segs, Skip{Count: 1000}, now, idx)
				if _, err := Apply(done, Skip{Count: 1}, now, idx); !errors.Is(err, ErrNothingToSkip) {
					t.Errorf("Apply = %v", err)
				}
			})
			t.Run("skip nothing", func(t *testing.T) {
				if _, err := Apply(segs, Skip{}, now, idx); !errors.Is(err, ErrNothingToSkip) {
					t.Errorf("Apply = %v", err)
				}
			})
			t.Run("jump forward", func(t *testing.T) {
				got, err := Apply(segs, Jump{Seq: 20}, now, idx)
				if err != nil {
					t.Fatal(err)
				}
				if got.FromSeq != 20 || !got.CreatedAt.Equal(now) || got.Reason != ReasonJump {
					t.Errorf("%+v", got)
				}
			})
			t.Run("jump to the position or behind it", func(t *testing.T) {
				for _, seq := range []int{released, released - 1, 0} {
					if _, err := Apply(segs, Jump{Seq: seq}, now, idx); !errors.Is(err, ErrJumpBackward) {
						t.Errorf("Jump(%d) = %v", seq, err)
					}
				}
			})
			t.Run("jump past the index", func(t *testing.T) {
				if _, err := Apply(segs, Jump{Seq: 50}, now, idx); !errors.Is(err, ErrNotIndexed) {
					t.Errorf("Apply = %v", err)
				}
			})
			t.Run("pause, then nothing moves", func(t *testing.T) {
				paused := mustApply(t, segs, Pause{}, now, idx)
				if got := mustReleased(t, paused, now.AddDate(1, 0, 0), idx); got != released {
					t.Errorf("released %d during a pause, want %d", got, released)
				}
				if _, err := Apply(paused, Pause{}, now, idx); !errors.Is(err, ErrAlreadyPaused) {
					t.Errorf("second pause: %v", err)
				}
			})
			t.Run("resume restores the pacing", func(t *testing.T) {
				paused := mustApply(t, segs, Pause{}, now, idx)
				resumed := mustApply(t, paused, Resume{}, now.AddDate(0, 0, 40), idx)
				last := resumed[len(resumed)-1]
				if last.Reason != ReasonResume || last.Pacing != pacing {
					t.Errorf("%+v", last)
				}
				if _, err := Apply(segs, Resume{}, now, idx); !errors.Is(err, ErrNotPaused) {
					t.Errorf("resume unpaused: %v", err)
				}
			})
			t.Run("skip while paused stays paused", func(t *testing.T) {
				paused := mustApply(t, segs, Pause{}, now, idx)
				skipped := mustApply(t, paused, Skip{Count: 2}, now.Add(time.Hour), idx)
				if _, ok := skipped[len(skipped)-1].Pacing.(Paused); !ok {
					t.Errorf("%+v", skipped[len(skipped)-1])
				}
				resumed := mustApply(t, skipped, Resume{}, now.Add(2*time.Hour), idx)
				if last := resumed[len(resumed)-1]; last.Pacing != pacing {
					t.Errorf("%+v", last)
				}
			})
		})
	}
}

func TestSwitchingPacing(t *testing.T) {
	idx := weekly(50)
	now := epoch.AddDate(0, 0, 3).Add(time.Hour)

	t.Run("rrule to replay anchors at the last release", func(t *testing.T) {
		segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
		released := mustReleased(t, segs, now, idx)
		last, err := ReleaseTime(segs, released, idx)
		if err != nil {
			t.Fatal(err)
		}
		// Speed 7 makes the next gap one day, and the last release was 19 hours ago.
		switched := mustApply(t, segs, Repace{ReplayPacing{Speed: 7}}, now, idx)
		next := switched[len(switched)-1]
		if !next.StartsAt.Equal(last) || next.Reason != ReasonPacing {
			t.Errorf("%+v, want it anchored at %s", next, last)
		}
		if at, _ := ReleaseTime(switched, released+1, idx); !at.Equal(last.Add(day)) {
			t.Errorf("next entry at %s", at)
		}
	})
	t.Run("replay to rrule counts from now", func(t *testing.T) {
		segs := []Segment{New(ReplayPacing{Speed: 7}, 0, 1, epoch)}
		switched := mustApply(t, segs, Repace{daily("UTC")}, now, idx)
		if last := switched[len(switched)-1]; !last.StartsAt.Equal(now) {
			t.Errorf("%+v", last)
		}
	})
	t.Run("a change after a long quiet spell releases one entry", func(t *testing.T) {
		segs := []Segment{New(Paused{}, 0, 1, epoch)}
		quiet := epoch.AddDate(0, 3, 0)
		switched := mustApply(t, segs, Repace{ReplayPacing{Speed: 7}}, quiet, idx)
		if got := mustReleased(t, switched, quiet, idx); got != 1 {
			t.Errorf("released %d at the change, want only the next entry", got)
		}
		if got := mustReleased(t, switched, quiet.Add(day-time.Second), idx); got != 1 {
			t.Errorf("released %d before the next gap ended", got)
		}
		if got := mustReleased(t, switched, quiet.Add(day), idx); got != 2 {
			t.Errorf("released %d after it", got)
		}
	})
	t.Run("with the next entry not indexed", func(t *testing.T) {
		short := weekly(1)
		segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
		switched := mustApply(t, segs, Repace{ReplayPacing{Speed: 7}}, now, short)
		if last := switched[len(switched)-1]; !last.StartsAt.Equal(epoch) {
			t.Errorf("anchor = %s, want the last release", last.StartsAt)
		}
	})
	t.Run("nothing released yet anchors at creation", func(t *testing.T) {
		segs := []Segment{New(daily("UTC"), 0, 0, epoch)}
		switched := mustApply(t, segs, Repace{ReplayPacing{Speed: 7}}, epoch.Add(time.Hour), idx)
		if last := switched[len(switched)-1]; !last.StartsAt.Equal(epoch) {
			t.Errorf("anchor = %s", last.StartsAt)
		}
	})
	t.Run("invalid pacing", func(t *testing.T) {
		segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
		if _, err := Apply(segs, Repace{Paused{}}, now, idx); !errors.Is(err, ErrInvalidPacing) {
			t.Errorf("Apply = %v", err)
		}
	})
}

func TestRhythmCarriesThroughAdjustments(t *testing.T) {
	// Every other day from Monday: Wednesday, Friday, Sunday.
	every2 := RRulePacing{RRule: "FREQ=DAILY;INTERVAL=2;BYHOUR=7;BYMINUTE=0;BYSECOND=0", TimeZone: "UTC", PerRelease: 1}
	idx := weekly(50)
	segs := []Segment{New(every2, 0, 1, epoch)}
	tuesday := epoch.Add(day)

	tests := map[string]Adjustment{"skip": Skip{Count: 1}, "jump": Jump{Seq: 5}, "same rule": Repace{every2}}
	for name, a := range tests {
		t.Run(name, func(t *testing.T) {
			after := mustApply(t, segs, a, tuesday, idx)
			next, ok, err := NextRelease(after, tuesday, idx)
			if err != nil || !ok {
				t.Fatalf("NextRelease: %v, %v", ok, err)
			}
			if want := time.Date(2026, 1, 7, 7, 0, 0, 0, time.UTC); !next.Equal(want) {
				t.Errorf("next = %s, want Wednesday, not Thursday", next)
			}
		})
	}
	t.Run("across a pause", func(t *testing.T) {
		paused := mustApply(t, segs, Pause{}, tuesday, idx)
		resumed := mustApply(t, paused, Resume{}, epoch.Add(4*day), idx) // Friday noon.
		next, ok, err := NextRelease(resumed, epoch.Add(4*day), idx)
		if err != nil || !ok {
			t.Fatalf("NextRelease: %v, %v", ok, err)
		}
		if want := time.Date(2026, 1, 11, 7, 0, 0, 0, time.UTC); !next.Equal(want) {
			t.Errorf("next = %s, want Sunday", next)
		}
	})
	t.Run("a new rule starts its own rhythm", func(t *testing.T) {
		other := RRulePacing{RRule: every2.RRule, TimeZone: "Europe/Dublin", PerRelease: 1}
		after := mustApply(t, segs, Repace{other}, tuesday, idx)
		if last := after[len(after)-1]; !last.StartsAt.Equal(tuesday) {
			t.Errorf("starts_at = %s, want now", last.StartsAt)
		}
	})
}

func TestResumeKeepsTheElapsedGap(t *testing.T) {
	idx := weekly(10)
	segs := []Segment{New(ReplayPacing{Speed: 1}, 0, 1, epoch)}
	pauseAt := epoch.Add(3 * day)
	paused := mustApply(t, segs, Pause{}, pauseAt, idx)
	resumeAt := pauseAt.Add(30 * day)
	resumed := mustApply(t, paused, Resume{}, resumeAt, idx)

	// Three days of the seven-day gap had passed, so four are left.
	at, err := ReleaseTime(resumed, 1, idx)
	if err != nil {
		t.Fatal(err)
	}
	if want := resumeAt.Add(4 * day); !at.Equal(want) {
		t.Errorf("entry 1 at %s, want %s", at, want)
	}
}

func TestSkipRestartsTheGapFromNow(t *testing.T) {
	idx := weekly(10)
	segs := []Segment{New(ReplayPacing{Speed: 1}, 0, 1, epoch)}
	skipAt := epoch.Add(3 * day)
	skipped := mustApply(t, segs, Skip{Count: 1}, skipAt, idx)
	at, err := ReleaseTime(skipped, 2, idx)
	if err != nil {
		t.Fatal(err)
	}
	if want := skipAt.Add(7 * day); !at.Equal(want) {
		t.Errorf("entry 2 at %s, want %s", at, want)
	}
}

func TestAClockSteppingBackKeepsSegmentsInOrder(t *testing.T) {
	idx := weekly(10)
	segs := []Segment{New(daily("UTC"), 0, 1, epoch)}
	s, err := Apply(segs, Pause{}, epoch.Add(-time.Hour), idx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.CreatedAt.Equal(epoch) {
		t.Errorf("created_at = %s", s.CreatedAt)
	}
}

type unknownAdjustment struct{}

func (unknownAdjustment) isAdjustment() {}

func TestUnknownAdjustment(t *testing.T) {
	idx := weekly(3)
	if _, err := Apply([]Segment{New(daily("UTC"), 0, 1, epoch)}, unknownAdjustment{}, epoch, idx); err == nil {
		t.Error("Apply accepted an unknown adjustment")
	}
}

func TestLastPacingWithNothingBeforeThePause(t *testing.T) {
	if _, ok := lastPacing([]Segment{{Pacing: Paused{}}}).(Paused); !ok {
		t.Error("want Paused")
	}
}
