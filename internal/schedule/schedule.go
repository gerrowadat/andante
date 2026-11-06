// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package schedule

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Errors an adjustment or a calculation can end in.
var (
	ErrNoSegments    = errors.New("a read has at least one segment")
	ErrNotStarted    = errors.New("the read had not started at that time")
	ErrNotScheduled  = errors.New("the entry has no release time")
	ErrJumpBackward  = errors.New("a read only moves forward")
	ErrNotIndexed    = errors.New("that entry is not indexed yet")
	ErrNothingToSkip = errors.New("there is nothing to skip to")
	ErrNotPaused     = errors.New("the read is not paused")
	ErrAlreadyPaused = errors.New("the read is already paused")
	ErrInvalidPacing = errors.New("invalid pacing")
)

// Pacing is how a segment releases entries.
type Pacing interface{ isPacing() }

// RRulePacing is a fixed schedule: PerRelease entries at each occurrence.
type RRulePacing struct {
	RRule, TimeZone string
	PerRelease      int
}

// ReplayPacing keeps the original run's gaps, divided by Speed. Zero MinGap and
// MaxGap mean no limit. DefaultGap stands in for a gap with an undated end.
type ReplayPacing struct {
	Speed                      float64
	MinGap, MaxGap, DefaultGap time.Duration
}

// Paused releases nothing.
type Paused struct{}

func (RRulePacing) isPacing()  {}
func (ReplayPacing) isPacing() {}
func (Paused) isPacing()       {}

// Reason says why a segment exists.
type Reason string

// The reasons a segment can have.
const (
	ReasonCreated Reason = "created"
	ReasonPacing  Reason = "pacing"
	ReasonPause   Reason = "pause"
	ReasonResume  Reason = "resume"
	ReasonSkip    Reason = "skip"
	ReasonJump    Reason = "jump"
)

// Segment is one row of a read's history: from CreatedAt on, with FromSeq already
// released, release entries by Pacing, counting from StartsAt. StartsAt can be
// earlier than CreatedAt.
type Segment struct {
	FromSeq   int
	StartsAt  time.Time
	CreatedAt time.Time
	Pacing    Pacing
	Reason    Reason
}

// Index is a view of a source's entries. Seqs are contiguous from Start to End,
// and discovery times do not go down as seq goes up.
type Index interface {
	Start() int // Lowest seq indexed.
	End() int   // Highest seq indexed. Below Start when empty.
	OriginalDate(seq int) (time.Time, bool)
	DiscoveredAt(seq int) time.Time
}

// Buffered holds back the last n entries, which is what catch_up = buffer does.
// An entry becomes available when the entry n after it is discovered.
func Buffered(idx Index, n int) Index {
	if n <= 0 {
		return idx
	}
	return buffered{Index: idx, n: n}
}

type buffered struct {
	Index
	n int
}

func (b buffered) End() int                       { return b.Index.End() - b.n }
func (b buffered) DiscoveredAt(seq int) time.Time { return b.Index.DiscoveredAt(seq + b.n) }

// activeAt returns the index of the last segment, in append order, in effect at t.
func activeAt(segs []Segment, t time.Time) (int, error) {
	if len(segs) == 0 {
		return 0, ErrNoSegments
	}
	active := -1
	for i, s := range segs {
		if !s.CreatedAt.After(t) {
			active = i
		}
	}
	if active < 0 {
		return 0, ErrNotStarted
	}
	return active, nil
}

// endAt returns the highest seq that had been discovered by t. Without this an
// entry found later would count as released at a time before andante knew of it.
func endAt(idx Index, t time.Time) int {
	start, end := idx.Start(), idx.End()
	if end < start {
		return end
	}
	if !idx.DiscoveredAt(end).After(t) {
		return end
	}
	n := sort.Search(end-start+1, func(i int) bool { return idx.DiscoveredAt(start + i).After(t) })
	return start + n - 1
}

// dueIn returns the last seq a segment's pacing says is due at t. Under rrule it
// can pass the end of the index.
func dueIn(s Segment, t time.Time, idx Index) (int, error) {
	switch p := s.Pacing.(type) {
	case RRulePacing:
		rule, err := compile(p, s.StartsAt)
		if err != nil {
			return 0, err
		}
		n, err := rule.count(t)
		if err != nil {
			return 0, err
		}
		return s.FromSeq + p.PerRelease*n, nil
	case ReplayPacing:
		due := s.FromSeq
		at := s.StartsAt
		for due < idx.End() {
			at = at.Add(p.gap(idx, due))
			if later(at, s.CreatedAt).After(t) {
				break
			}
			due++
		}
		return due, nil
	case Paused:
		return s.FromSeq, nil
	}
	return 0, fmt.Errorf("%w: %T", ErrInvalidPacing, s.Pacing)
}

// Due returns the last seq the pacing says is due at t, which can pass the end
// of the index.
func Due(segs []Segment, t time.Time, idx Index) (int, error) {
	i, err := activeAt(segs, t)
	if err != nil {
		return 0, err
	}
	return dueIn(segs[i], t, idx)
}

// Released returns the last seq released at t: what is due, as far as the index
// reached at t.
func Released(segs []Segment, t time.Time, idx Index) (int, error) {
	due, err := Due(segs, t, idx)
	if err != nil {
		return 0, err
	}
	return min(due, endAt(idx, t)), nil
}

// scheduled returns when a segment's pacing makes seq due, and whether it ever does.
func scheduled(s Segment, seq int, idx Index) (time.Time, bool, error) {
	switch p := s.Pacing.(type) {
	case RRulePacing:
		rule, err := compile(p, s.StartsAt)
		if err != nil {
			return time.Time{}, false, err
		}
		return rule.nth((seq - s.FromSeq + p.PerRelease - 1) / p.PerRelease)
	case ReplayPacing:
		at := s.StartsAt
		for q := s.FromSeq; q < seq; q++ {
			at = at.Add(p.gap(idx, q))
		}
		return later(at, s.CreatedAt), true, nil
	case Paused:
		return time.Time{}, false, nil
	}
	return time.Time{}, false, fmt.Errorf("%w: %T", ErrInvalidPacing, s.Pacing)
}

// ReleaseTime returns when seq is released, which can be in the future. It walks
// the segments to the one that released seq. Segments are never edited, so the
// answer never changes. It returns ErrNotScheduled for an entry that no pacing
// reaches: one past the index, or while paused.
func ReleaseTime(segs []Segment, seq int, idx Index) (time.Time, error) {
	if len(segs) == 0 {
		return time.Time{}, ErrNoSegments
	}
	if seq > idx.End() {
		return time.Time{}, ErrNotScheduled
	}
	discovered := idx.DiscoveredAt(seq)
	for i, s := range segs {
		// Inside from_seq: there as soon as the segment is, or as it is found.
		if seq <= s.FromSeq {
			return later(s.CreatedAt, discovered), nil
		}
		at, ok, err := scheduled(s, seq, idx)
		if err != nil {
			return time.Time{}, err
		}
		if !ok {
			continue
		}
		at = later(at, discovered)
		if rest := segs[i+1:]; len(rest) == 0 || at.Before(rest[0].CreatedAt) {
			return at, nil
		}
	}
	return time.Time{}, ErrNotScheduled
}

// NextRelease returns when the next entry after t is released, if that is known.
// It is not known at the end of the index, or while paused.
func NextRelease(segs []Segment, t time.Time, idx Index) (time.Time, bool, error) {
	released, err := Released(segs, t, idx)
	if err != nil {
		return time.Time{}, false, err
	}
	at, err := ReleaseTime(segs, released+1, idx)
	if errors.Is(err, ErrNotScheduled) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return at, true, nil
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
