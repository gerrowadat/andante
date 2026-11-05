// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"

	"github.com/gerrowadat/andante/internal/api/gen"
)

// These operations are in the contract, and return 501 until their phase.

func (h *handler) RequestCrawl(context.Context, gen.RequestCrawlRequestObject) (gen.RequestCrawlResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) CreateProbe(context.Context, gen.CreateProbeRequestObject) (gen.CreateProbeResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) ListReads(context.Context, gen.ListReadsRequestObject) (gen.ListReadsResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) CreateRead(context.Context, gen.CreateReadRequestObject) (gen.CreateReadResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) GetRead(context.Context, gen.GetReadRequestObject) (gen.GetReadResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) UpdateRead(context.Context, gen.UpdateReadRequestObject) (gen.UpdateReadResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) DeleteRead(context.Context, gen.DeleteReadRequestObject) (gen.DeleteReadResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) CreateAdjustment(context.Context, gen.CreateAdjustmentRequestObject) (gen.CreateAdjustmentResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) ListSegments(context.Context, gen.ListSegmentsRequestObject) (gen.ListSegmentsResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) ListFeedTokens(context.Context, gen.ListFeedTokensRequestObject) (gen.ListFeedTokensResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) CreateFeedToken(context.Context, gen.CreateFeedTokenRequestObject) (gen.CreateFeedTokenResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) RevokeFeedToken(context.Context, gen.RevokeFeedTokenRequestObject) (gen.RevokeFeedTokenResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) ExportOPML(context.Context, gen.ExportOPMLRequestObject) (gen.ExportOPMLResponseObject, error) {
	return nil, notImplemented()
}

func (h *handler) ImportOPML(context.Context, gen.ImportOPMLRequestObject) (gen.ImportOPMLResponseObject, error) {
	return nil, notImplemented()
}
