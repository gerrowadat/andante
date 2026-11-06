// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import "github.com/gerrowadat/andante/internal/httpapi"

const (
	problemInvalidRead = httpapi.TypeInvalidRead
	problemConflict    = httpapi.TypeConflict
)

var (
	newProblem     = httpapi.NewProblem
	notFound       = httpapi.NotFound
	invalidRequest = httpapi.InvalidRequest
)
