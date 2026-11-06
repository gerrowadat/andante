// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"errors"
	"fmt"
	"time"
)

// Adjustment is a change to a read. Each appends a segment.
type Adjustment interface{ isAdjustment() }

// Skip releases the next Count entries now.
type Skip struct{ Count int }

// Pause stops releases.
type Pause struct{}

// Resume restarts releases with the pacing from before the pause.
type Resume struct{}

// Jump releases everything up to Seq now.
type Jump struct{ Seq int }

// Repace changes the pacing.
type Repace struct{ Pacing Pacing }

func (Skip) isAdjustment()   {}
func (Pause) isAdjustment()  {}
func (Resume) isAdjustment() {}
func (Jump) isAdjustment()   {}
func (Repace) isAdjustment() {}

// New returns a read's first segment. initial entries are there immediately.
func New(p Pacing, startSeq, initial int, now time.Time) Segment {
	return Segment{
		FromSeq:   startSeq - 1 + initial,
		StartsAt:  now,
		CreatedAt: now,
		Pacing:    p,
		Reason:    ReasonCreated,
	}
}

// Apply returns the segment an adjustment appends.
func Apply(segs []Segment, adjustment Adjustment, now time.Time, idx Index) (Segment, error) {
	if len(segs) == 0 {
		return Segment{}, ErrNoSegments
	}
	current := segs[len(segs)-1]
	// Segments stay in order even if the clock stepped back.
	now = later(now, current.CreatedAt)
	released, err := Released(segs, now, idx)
	if err != nil {
		return Segment{}, err
	}
	end := endAt(idx, now)

	switch a := adjustment.(type) {
	case Skip:
		target := min(released+a.Count, end)
		if a.Count < 1 || target <= released {
			return Segment{}, ErrNothingToSkip
		}
		starts, err := startFor(segs, current.Pacing, now)
		if err != nil {
			return Segment{}, err
		}
		return Segment{target, starts, now, current.Pacing, ReasonSkip}, nil

	case Jump:
		if a.Seq <= released {
			return Segment{}, ErrJumpBackward
		}
		if a.Seq > end {
			return Segment{}, ErrNotIndexed
		}
		starts, err := startFor(segs, current.Pacing, now)
		if err != nil {
			return Segment{}, err
		}
		return Segment{a.Seq, starts, now, current.Pacing, ReasonJump}, nil

	case Pause:
		if _, paused := current.Pacing.(Paused); paused {
			return Segment{}, ErrAlreadyPaused
		}
		return Segment{released, now, now, Paused{}, ReasonPause}, nil

	case Resume:
		if _, paused := current.Pacing.(Paused); !paused {
			return Segment{}, ErrNotPaused
		}
		pacing := lastPacing(segs)
		starts, err := startFor(segs, pacing, now)
		if err != nil {
			return Segment{}, err
		}
		if _, replay := pacing.(ReplayPacing); replay {
			elapsed := current.StartsAt.Sub(anchor(segs, released, idx))
			starts = now.Add(-elapsed)
		}
		return Segment{released, starts, now, pacing, ReasonResume}, nil

	case Repace:
		if err := ValidatePacing(a.Pacing); err != nil {
			return Segment{}, err
		}
		starts, err := startFor(segs, a.Pacing, now)
		if err != nil {
			return Segment{}, err
		}
		if p, replay := a.Pacing.(ReplayPacing); replay {
			starts = anchor(segs, released, idx)
			// Only the next entry can be overdue, so a change of pacing does not
			// release a backlog.
			if released+1 <= idx.End() {
				starts = later(starts, now.Add(-p.gap(idx, released)))
			}
		}
		return Segment{released, starts, now, a.Pacing, ReasonPacing}, nil
	}
	return Segment{}, fmt.Errorf("unknown adjustment %T", adjustment)
}

// startFor returns what a new segment under pacing counts from. That is now,
// except for a rule the read is already on. "Every other day" is counted from a
// rule's start, so restarting it now could move every later occurrence by a day,
// and a skip has no business doing that. Such a rule carries on from its last
// occurrence, which keeps its rhythm.
func startFor(segs []Segment, pacing Pacing, now time.Time) (time.Time, error) {
	p, ok := pacing.(RRulePacing)
	if !ok {
		return now, nil
	}
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i].Pacing != pacing {
			continue
		}
		r, err := compile(p, segs[i].StartsAt)
		if err != nil {
			return time.Time{}, err
		}
		n, err := r.count(now)
		if err != nil {
			return time.Time{}, err
		}
		if n == 0 {
			return segs[i].StartsAt, nil
		}
		at, _, err := r.nth(n)
		return at, err
	}
	return now, nil
}

// anchor returns when seq was released, which is what a gap after it counts from.
// If nothing has been released, that is when the read began.
func anchor(segs []Segment, seq int, idx Index) time.Time {
	if seq < idx.Start() || seq > idx.End() {
		return segs[0].CreatedAt
	}
	at, err := ReleaseTime(segs, seq, idx)
	if errors.Is(err, ErrNotScheduled) {
		return segs[0].CreatedAt
	}
	return at
}

// lastPacing returns the last pacing that was not a pause.
func lastPacing(segs []Segment) Pacing {
	for i := len(segs) - 1; i >= 0; i-- {
		if _, paused := segs[i].Pacing.(Paused); !paused {
			return segs[i].Pacing
		}
	}
	return Paused{}
}
