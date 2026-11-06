// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package reads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Errors a caller can act on.
var (
	ErrSourceNotFound = errors.New("the source does not exist")
	ErrTooFewDates    = errors.New("too few of the source's entries have dates for replay")
	// ErrFixedCatchUp means a read's catch_up or buffer cannot change. Release
	// times depend on them, and a release time never changes.
	ErrFixedCatchUp = errors.New("catch_up and buffer are fixed for the life of a read")
)

const (
	catchUpPassthrough = "passthrough"
	catchUpBuffer      = "buffer"

	// State values, as the API reports them.
	StateActive   = "active"
	StatePaused   = "paused"
	StateCaughtUp = "caught-up"

	pacingRRule  = "rrule"
	pacingReplay = "replay"
	pacingPaused = "paused"
)

// Options configure a Service.
type Options struct {
	Store           *store.Store
	DefaultTimezone string
	MinDatedShare   float64 // Below this, replay needs a default_gap.
	Now             func() time.Time
}

// Service manages reads.
type Service struct {
	opts Options
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts}
}

// View is a read with its position worked out.
type View struct {
	Read        db.Read
	Position    int
	Due         int
	NextRelease *time.Time
	IndexEnd    *int
	State       string
}

// CreateParams describe a new read.
type CreateParams struct {
	SourceID int64
	Name     string
	Pacing   schedule.Pacing
	StartSeq *int
	Initial  int
	CatchUp  string
	Buffer   int
	Config   json.RawMessage
}

// PatchParams change a read. Nil means leave alone.
type PatchParams struct {
	Name    *string
	Config  json.RawMessage
	CatchUp *string
	Buffer  *int
}

// index is a schedule.Index over a source's entries.
type index struct {
	rows []db.ListEntryTimesRow
}

func (i index) Start() int {
	if len(i.rows) == 0 {
		return 0
	}
	return int(i.rows[0].Seq)
}

func (i index) End() int { return i.Start() + len(i.rows) - 1 }

func (i index) row(seq int) (db.ListEntryTimesRow, bool) {
	at := seq - i.Start()
	if at < 0 || at >= len(i.rows) {
		return db.ListEntryTimesRow{}, false
	}
	return i.rows[at], true
}

func (i index) OriginalDate(seq int) (time.Time, bool) {
	row, ok := i.row(seq)
	if !ok || row.OriginalDate == nil {
		return time.Time{}, false
	}
	return *row.OriginalDate, true
}

func (i index) DiscoveredAt(seq int) time.Time {
	row, ok := i.row(seq)
	if !ok {
		return time.Time{}
	}
	return row.DiscoveredAt
}

func (i index) datedShare() float64 {
	if len(i.rows) == 0 {
		return 0
	}
	dated := 0
	for _, row := range i.rows {
		if row.OriginalDate != nil {
			dated++
		}
	}
	return float64(dated) / float64(len(i.rows))
}

// view applies a read's catch-up setting to its index.
func view(read db.Read, rows []db.ListEntryTimesRow) schedule.Index {
	idx := index{rows: rows}
	if read.CatchUp == catchUpBuffer {
		return schedule.Buffered(idx, int(read.Buffer))
	}
	return idx
}

// checkReplay refuses replay on a source with too few dates, unless the read says
// what to do about the rest.
func (s *Service) checkReplay(p schedule.Pacing, rows []db.ListEntryTimesRow) error {
	replay, ok := p.(schedule.ReplayPacing)
	if !ok || replay.DefaultGap > 0 {
		return nil
	}
	if (index{rows: rows}).datedShare() < s.opts.MinDatedShare {
		return ErrTooFewDates
	}
	return nil
}

// Create makes a read and its first segment.
func (s *Service) Create(ctx context.Context, params CreateParams) (View, error) {
	if err := schedule.ValidatePacing(params.Pacing); err != nil {
		return View{}, err
	}
	rows, err := s.opts.Store.EntryTimes(ctx, params.SourceID)
	if err != nil {
		return View{}, err
	}
	if _, err := s.opts.Store.GetSource(ctx, params.SourceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return View{}, fmt.Errorf("%w: %d", ErrSourceNotFound, params.SourceID)
		}
		return View{}, err
	}
	if err := s.checkReplay(params.Pacing, rows); err != nil {
		return View{}, err
	}

	start := (index{rows: rows}).Start()
	if params.StartSeq != nil {
		start = *params.StartSeq
	}
	catchUp := params.CatchUp
	if catchUp == "" {
		catchUp = catchUpPassthrough
	}
	config := params.Config
	if config == nil {
		config = json.RawMessage("{}")
	}
	segment, err := s.toRow(schedule.New(params.Pacing, start, params.Initial, s.opts.Now().UTC()))
	if err != nil {
		return View{}, err
	}
	read, err := s.opts.Store.CreateRead(ctx, db.InsertReadParams{
		SourceID: params.SourceID,
		Name:     params.Name,
		CatchUp:  catchUp,
		Buffer:   int32(params.Buffer), //nolint:gosec // The API bounds it to an int32.
		Config:   config,
		StartSeq: int32(start), //nolint:gosec // Seqs are int32 in the database.
	}, segment)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, read)
}

// Get returns a read with its position.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	read, err := s.opts.Store.GetRead(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, read)
}

// List returns reads with their positions.
func (s *Service) List(ctx context.Context, after int64, limit int32) ([]View, error) {
	reads, err := s.opts.Store.ListReads(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(reads))
	for _, read := range reads {
		v, err := s.view(ctx, read)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

// Update changes a read's name and config.
func (s *Service) Update(ctx context.Context, id int64, patch PatchParams) (View, error) {
	read, err := s.opts.Store.GetRead(ctx, id)
	if err != nil {
		return View{}, err
	}
	if (patch.CatchUp != nil && *patch.CatchUp != read.CatchUp) ||
		(patch.Buffer != nil && *patch.Buffer != int(read.Buffer)) {
		return View{}, ErrFixedCatchUp
	}
	params := db.UpdateReadParams{ID: id, Name: read.Name, Config: read.Config}
	if patch.Name != nil {
		params.Name = *patch.Name
	}
	if patch.Config != nil {
		params.Config = patch.Config
	}
	updated, err := s.opts.Store.UpdateRead(ctx, params)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, updated)
}

// Delete removes a read.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.opts.Store.DeleteRead(ctx, id)
}

// Adjust appends the segment an adjustment makes. Two adjustments to one read
// happen one after the other.
func (s *Service) Adjust(ctx context.Context, id int64, adjustment schedule.Adjustment) (View, error) {
	err := s.opts.Store.AppendSegment(ctx, id, func(read db.Read, rows []db.ReadSegment, times []db.ListEntryTimesRow) (db.InsertSegmentParams, error) {
		segments, err := fromRows(rows)
		if err != nil {
			return db.InsertSegmentParams{}, err
		}
		if repace, ok := adjustment.(schedule.Repace); ok {
			if err := s.checkReplay(repace.Pacing, times); err != nil {
				return db.InsertSegmentParams{}, err
			}
		}
		segment, err := schedule.Apply(segments, adjustment, s.opts.Now().UTC(), view(read, times))
		if err != nil {
			return db.InsertSegmentParams{}, err
		}
		return s.toRow(segment)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) view(ctx context.Context, read db.Read) (View, error) {
	rows, err := s.opts.Store.Segments(ctx, read.ID)
	if err != nil {
		return View{}, err
	}
	times, err := s.opts.Store.EntryTimes(ctx, read.SourceID)
	if err != nil {
		return View{}, err
	}
	segments, err := fromRows(rows)
	if err != nil {
		return View{}, err
	}
	return position(read, segments, view(read, times), s.opts.Now().UTC())
}

func position(read db.Read, segments []schedule.Segment, idx schedule.Index, now time.Time) (View, error) {
	// The clock can step back past the last segment. Nothing moves until it catches up.
	if last := segments[len(segments)-1].CreatedAt; now.Before(last) {
		now = last
	}
	released, err := schedule.Released(segments, now, idx)
	if err != nil {
		return View{}, err
	}
	due, err := schedule.Due(segments, now, idx)
	if err != nil {
		return View{}, err
	}
	v := View{Read: read, Position: released, Due: due, State: StateActive}
	if at, ok, err := schedule.NextRelease(segments, now, idx); err != nil {
		return View{}, err
	} else if ok {
		v.NextRelease = &at
	}
	if end := idx.End(); end >= idx.Start() {
		v.IndexEnd = &end
	}
	switch _, paused := segments[len(segments)-1].Pacing.(schedule.Paused); {
	case paused:
		v.State = StatePaused
	case released >= idx.End():
		v.State = StateCaughtUp
	}
	return v, nil
}

// Furthest returns the furthest due position of a source's reads, which the
// crawl loop keeps the index ahead of.
func (s *Service) Furthest(ctx context.Context, sourceID int64) (int32, bool, error) {
	all, err := s.opts.Store.ListReadsOfSource(ctx, sourceID)
	if err != nil {
		return 0, false, err
	}
	var (
		furthest int
		found    bool
	)
	for _, read := range all {
		v, err := s.view(ctx, read)
		if err != nil {
			return 0, false, err
		}
		if !found || v.Due > furthest {
			furthest, found = v.Due, true
		}
	}
	return int32(furthest), found, nil //nolint:gosec // Seqs are int32 in the database.
}

// Segments returns a page of a read's history.
func (s *Service) Segments(ctx context.Context, id, after int64, limit int32) ([]db.ReadSegment, error) {
	return s.opts.Store.SegmentsPage(ctx, id, after, limit)
}

func interval(d time.Duration) pgtype.Interval {
	if d == 0 {
		return pgtype.Interval{}
	}
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

func duration(i pgtype.Interval) time.Duration {
	if !i.Valid {
		return 0
	}
	return time.Duration(i.Microseconds)*time.Microsecond +
		time.Duration(i.Days)*24*time.Hour
}

// toRow turns a segment into what the database stores.
func (s *Service) toRow(segment schedule.Segment) (db.InsertSegmentParams, error) {
	row := db.InsertSegmentParams{
		FromSeq:    int32(segment.FromSeq), //nolint:gosec // Seqs are int32 in the database.
		StartsAt:   segment.StartsAt,
		CreatedAt:  segment.CreatedAt,
		Timezone:   s.opts.DefaultTimezone,
		PerRelease: 1,
		Reason:     string(segment.Reason),
	}
	switch p := segment.Pacing.(type) {
	case schedule.RRulePacing:
		row.Pacing, row.Rrule, row.Timezone = pacingRRule, &p.RRule, p.TimeZone
		row.PerRelease = int32(p.PerRelease) //nolint:gosec // The API bounds it to an int32.
	case schedule.ReplayPacing:
		row.Pacing, row.Speed = pacingReplay, &p.Speed
		row.MinGap, row.MaxGap, row.DefaultGap = interval(p.MinGap), interval(p.MaxGap), interval(p.DefaultGap)
	case schedule.Paused:
		row.Pacing = pacingPaused
	default:
		return db.InsertSegmentParams{}, fmt.Errorf("unknown pacing %T", segment.Pacing)
	}
	return row, nil
}

// PacingOf turns a stored segment's pacing back into its own type.
func PacingOf(row db.ReadSegment) (schedule.Pacing, error) {
	switch row.Pacing {
	case pacingRRule:
		if row.Rrule == nil {
			return nil, fmt.Errorf("segment %d has no rrule", row.ID)
		}
		return schedule.RRulePacing{RRule: *row.Rrule, TimeZone: row.Timezone, PerRelease: int(row.PerRelease)}, nil
	case pacingReplay:
		if row.Speed == nil {
			return nil, fmt.Errorf("segment %d has no speed", row.ID)
		}
		return schedule.ReplayPacing{
			Speed:      *row.Speed,
			MinGap:     duration(row.MinGap),
			MaxGap:     duration(row.MaxGap),
			DefaultGap: duration(row.DefaultGap),
		}, nil
	case pacingPaused:
		return schedule.Paused{}, nil
	}
	return nil, fmt.Errorf("segment %d has unknown pacing %q", row.ID, row.Pacing)
}

func fromRows(rows []db.ReadSegment) ([]schedule.Segment, error) {
	segments := make([]schedule.Segment, 0, len(rows))
	for _, row := range rows {
		pacing, err := PacingOf(row)
		if err != nil {
			return nil, err
		}
		segments = append(segments, schedule.Segment{
			FromSeq:   int(row.FromSeq),
			StartsAt:  row.StartsAt,
			CreatedAt: row.CreatedAt,
			Pacing:    pacing,
			Reason:    schedule.Reason(row.Reason),
		})
	}
	return segments, nil
}
