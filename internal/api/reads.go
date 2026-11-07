// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/oapi-codegen/nullable"
	"github.com/oapi-codegen/runtime/types"
)

// readError turns what the reads service refuses into problem details.
func readError(id int64, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("read", id)
	case errors.Is(err, schedule.ErrJumpBackward), errors.Is(err, schedule.ErrNotIndexed),
		errors.Is(err, schedule.ErrNothingToSkip), errors.Is(err, schedule.ErrNotPaused),
		errors.Is(err, schedule.ErrAlreadyPaused), errors.Is(err, reads.ErrFixedCatchUp):
		return newProblem(http.StatusConflict, problemConflict, err.Error())
	case errors.Is(err, schedule.ErrInvalidPacing), errors.Is(err, reads.ErrTooFewDates),
		errors.Is(err, reads.ErrSourceNotFound):
		return newProblem(http.StatusUnprocessableEntity, problemInvalidRead, err.Error())
	}
	return err
}

func optionalDuration(d *types.Duration, name string) (time.Duration, error) {
	if d == nil {
		return 0, nil
	}
	converted, err := d.TimeDuration()
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", schedule.ErrInvalidPacing, name, err)
	}
	return converted, nil
}

func pacingFromAPI(p client.Pacing) (schedule.Pacing, error) {
	mode, err := p.Discriminator()
	if err != nil {
		return nil, fmt.Errorf("read pacing mode: %w", err)
	}
	switch mode {
	case string(client.Rrule):
		rule, err := p.AsRRulePacing()
		if err != nil {
			return nil, fmt.Errorf("decode pacing: %w", err)
		}
		pacing := schedule.RRulePacing{RRule: rule.Rrule, TimeZone: rule.Timezone, PerRelease: 1}
		if rule.PerRelease != nil {
			pacing.PerRelease = int(*rule.PerRelease)
		}
		return pacing, nil
	case string(client.Replay):
		replay, err := p.AsReplayPacing()
		if err != nil {
			return nil, fmt.Errorf("decode pacing: %w", err)
		}
		pacing := schedule.ReplayPacing{Speed: replay.Speed}
		if pacing.MinGap, err = optionalDuration(replay.MinGap, "min_gap"); err != nil {
			return nil, err
		}
		if pacing.MaxGap, err = optionalDuration(replay.MaxGap, "max_gap"); err != nil {
			return nil, err
		}
		if pacing.DefaultGap, err = optionalDuration(replay.DefaultGap, "default_gap"); err != nil {
			return nil, err
		}
		return pacing, nil
	}
	return nil, fmt.Errorf("%w: pause a read with a pause adjustment", schedule.ErrInvalidPacing)
}

func gapToAPI(d time.Duration) (*types.Duration, error) {
	if d == 0 {
		return nil, nil //nolint:nilnil // No gap is a normal answer.
	}
	converted, err := types.FromTimeDuration(d)
	if err != nil {
		return nil, fmt.Errorf("convert gap: %w", err)
	}
	return &converted, nil
}

func pacingToAPI(p schedule.Pacing) (client.Pacing, error) {
	var out client.Pacing
	switch p := p.(type) {
	case schedule.RRulePacing:
		per := int32(p.PerRelease) //nolint:gosec // It came in as an int32.
		return out, out.FromRRulePacing(client.RRulePacing{Mode: client.Rrule, Rrule: p.RRule, Timezone: p.TimeZone, PerRelease: &per})
	case schedule.ReplayPacing:
		replay := client.ReplayPacing{Mode: client.Replay, Speed: p.Speed}
		var err error
		if replay.MinGap, err = gapToAPI(p.MinGap); err != nil {
			return out, err
		}
		if replay.MaxGap, err = gapToAPI(p.MaxGap); err != nil {
			return out, err
		}
		if replay.DefaultGap, err = gapToAPI(p.DefaultGap); err != nil {
			return out, err
		}
		return out, out.FromReplayPacing(replay)
	}
	return out, out.FromPausedPacing(client.PausedPacing{Mode: client.PausedPacingModePaused})
}

func readToAPI(v reads.View) (client.Read, error) {
	var config map[string]any
	if err := json.Unmarshal(v.Read.Config, &config); err != nil {
		return client.Read{}, fmt.Errorf("decode config of read %d: %w", v.Read.ID, err)
	}
	read := client.Read{
		ID:            v.Read.ID,
		UserID:        v.Read.UserID,
		SourceID:      v.Read.SourceID,
		Name:          v.Read.Name,
		CatchUp:       client.CatchUp(v.Read.CatchUp),
		Buffer:        v.Read.Buffer,
		Config:        config,
		CreatedAt:     v.Read.CreatedAt,
		Position:      int32(v.Position), //nolint:gosec // Seqs are int32 in the database.
		Due:           clampToInt32(v.Due),
		NextReleaseAt: nullableValue(v.NextRelease),
		IndexEnd:      nullable.NewNullNullable[int32](),
		State:         client.ReadState(v.State),
	}
	if v.IndexEnd != nil {
		read.IndexEnd = nullable.NewNullableWithValue(int32(*v.IndexEnd)) //nolint:gosec // As above.
	}
	return read, nil
}

// clampToInt32 keeps a due position that has run far past the index in range.
func clampToInt32(n int) int32 {
	const maxInt32 = 1<<31 - 1
	return int32(min(n, maxInt32)) //nolint:gosec // Just bounded.
}

func (h *handler) ListReads(ctx context.Context, r gen.ListReadsRequestObject) (gen.ListReadsResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	views, err := h.reads.List(ctx, actorOf(ctx), r.Params.All != nil && *r.Params.All, p.after, p.fetchLimit())
	if err != nil {
		return nil, err
	}
	key := func(v reads.View) int64 { return v.Read.ID }
	result, err := paginate(p, "/reads", views, key, readToAPI)
	if err != nil {
		return nil, err
	}
	return gen.ListReads200JSONResponse{
		Body:    client.ReadList{Items: result.items, Next: result.next},
		Headers: gen.ListReads200ResponseHeaders{Link: result.link},
	}, nil
}

func (h *handler) CreateRead(ctx context.Context, r gen.CreateReadRequestObject) (gen.CreateReadResponseObject, error) {
	pacing, err := pacingFromAPI(r.Body.Pacing)
	if err != nil {
		return nil, readError(0, err)
	}
	params := reads.CreateParams{UserID: actorOf(ctx).UserID, SourceID: r.Body.SourceID, Name: r.Body.Name, Pacing: pacing, Initial: 1}
	if r.Body.StartSeq != nil {
		start := int(*r.Body.StartSeq)
		params.StartSeq = &start
	}
	if r.Body.Initial != nil {
		params.Initial = int(*r.Body.Initial)
	}
	if r.Body.CatchUp != nil {
		params.CatchUp = string(*r.Body.CatchUp)
	}
	if r.Body.Buffer != nil {
		params.Buffer = int(*r.Body.Buffer)
	}
	if r.Body.Config != nil {
		if params.Config, err = json.Marshal(*r.Body.Config); err != nil {
			return nil, fmt.Errorf("encode config: %w", err)
		}
	}
	created, err := h.reads.Create(ctx, params)
	if err != nil {
		return nil, readError(0, err)
	}
	body, err := readToAPI(created)
	if err != nil {
		return nil, err
	}
	h.demandChanged(ctx, created.Read.SourceID)
	return gen.CreateRead201JSONResponse(body), nil
}

func (h *handler) GetRead(ctx context.Context, r gen.GetReadRequestObject) (gen.GetReadResponseObject, error) {
	found, err := h.reads.Get(ctx, actorOf(ctx), r.ID)
	if err != nil {
		return nil, readError(r.ID, err)
	}
	body, err := readToAPI(found)
	if err != nil {
		return nil, err
	}
	return gen.GetRead200JSONResponse(body), nil
}

func (h *handler) UpdateRead(ctx context.Context, r gen.UpdateReadRequestObject) (gen.UpdateReadResponseObject, error) {
	patch := reads.PatchParams{Name: r.Body.Name}
	if r.Body.CatchUp != nil {
		catchUp := string(*r.Body.CatchUp)
		patch.CatchUp = &catchUp
	}
	if r.Body.Buffer != nil {
		buffer := int(*r.Body.Buffer)
		patch.Buffer = &buffer
	}
	if r.Body.Config != nil {
		var err error
		if patch.Config, err = json.Marshal(*r.Body.Config); err != nil {
			return nil, fmt.Errorf("encode config: %w", err)
		}
	}
	updated, err := h.reads.Update(ctx, actorOf(ctx), r.ID, patch)
	if err != nil {
		return nil, readError(r.ID, err)
	}
	body, err := readToAPI(updated)
	if err != nil {
		return nil, err
	}
	return gen.UpdateRead200JSONResponse(body), nil
}

func (h *handler) DeleteRead(ctx context.Context, r gen.DeleteReadRequestObject) (gen.DeleteReadResponseObject, error) {
	found, err := h.reads.Get(ctx, actorOf(ctx), r.ID)
	if err != nil {
		return nil, readError(r.ID, err)
	}
	if err := h.reads.Delete(ctx, actorOf(ctx), r.ID); err != nil {
		return nil, readError(r.ID, err)
	}
	h.demandChanged(ctx, found.Read.SourceID)
	return gen.DeleteRead204Response{}, nil
}

func adjustmentFromAPI(a client.Adjustment) (schedule.Adjustment, error) {
	kind, err := a.Discriminator()
	if err != nil {
		return nil, fmt.Errorf("read adjustment type: %w", err)
	}
	switch kind {
	case string(client.SkipAdjustmentTypeSkip):
		skip, err := a.AsSkipAdjustment()
		if err != nil {
			return nil, fmt.Errorf("decode adjustment: %w", err)
		}
		count := 1
		if skip.Count != nil {
			count = int(*skip.Count)
		}
		return schedule.Skip{Count: count}, nil
	case string(client.PauseAdjustmentTypePause):
		return schedule.Pause{}, nil
	case string(client.ResumeAdjustmentTypeResume):
		return schedule.Resume{}, nil
	case string(client.JumpAdjustmentTypeJump):
		jump, err := a.AsJumpAdjustment()
		if err != nil {
			return nil, fmt.Errorf("decode adjustment: %w", err)
		}
		return schedule.Jump{Seq: int(jump.Seq)}, nil
	}
	repace, err := a.AsPacingAdjustment()
	if err != nil {
		return nil, fmt.Errorf("decode adjustment: %w", err)
	}
	pacing, err := pacingFromAPI(repace.Pacing)
	if err != nil {
		return nil, err
	}
	return schedule.Repace{Pacing: pacing}, nil
}

func (h *handler) CreateAdjustment(ctx context.Context, r gen.CreateAdjustmentRequestObject) (gen.CreateAdjustmentResponseObject, error) {
	adjustment, err := adjustmentFromAPI(*r.Body)
	if err != nil {
		return nil, readError(r.ID, err)
	}
	adjusted, err := h.reads.Adjust(ctx, actorOf(ctx), r.ID, adjustment)
	if err != nil {
		return nil, readError(r.ID, err)
	}
	body, err := readToAPI(adjusted)
	if err != nil {
		return nil, err
	}
	h.demandChanged(ctx, adjusted.Read.SourceID)
	return gen.CreateAdjustment200JSONResponse(body), nil
}

func segmentToAPI(row db.ReadSegment) (client.Segment, error) {
	pacing, err := reads.PacingOf(row)
	if err != nil {
		return client.Segment{}, err
	}
	converted, err := pacingToAPI(pacing)
	if err != nil {
		return client.Segment{}, err
	}
	return client.Segment{
		ID:        row.ID,
		FromSeq:   row.FromSeq,
		StartsAt:  row.StartsAt,
		CreatedAt: row.CreatedAt,
		Reason:    client.SegmentReason(row.Reason),
		Pacing:    converted,
	}, nil
}

func (h *handler) ListSegments(ctx context.Context, r gen.ListSegmentsRequestObject) (gen.ListSegmentsResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.reads.Segments(ctx, actorOf(ctx), r.ID, p.after, p.fetchLimit())
	if err != nil {
		return nil, readError(r.ID, err)
	}
	key := func(s db.ReadSegment) int64 { return s.ID }
	path := "/reads/" + strconv.FormatInt(r.ID, 10) + "/segments"
	result, err := paginate(p, path, rows, key, segmentToAPI)
	if err != nil {
		return nil, err
	}
	return gen.ListSegments200JSONResponse{
		Body:    client.SegmentList{Items: result.items, Next: result.next},
		Headers: gen.ListSegments200ResponseHeaders{Link: result.link},
	}, nil
}

// demandChanged tells the catalogue straight away, so that a new read doesn't wait
// for the next round. The next round fixes a failure, so a read doesn't depend on it.
func (h *handler) demandChanged(ctx context.Context, sourceID int64) {
	if err := h.sync.PushDemand(ctx, sourceID); err != nil {
		h.logger.Warn("could not report demand", "source", sourceID, "error", err)
	}
}
