// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"

	"github.com/gerrowadat/andante/internal/api/gen"
)

// These operations are in the contract, and return 501 until their phase.

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
