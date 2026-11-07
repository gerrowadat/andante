// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"net/http"

	"github.com/gerrowadat/andante/internal/httpapi"
)

const (
	problemInvalidRead = httpapi.TypeInvalidRead
	problemConflict    = httpapi.TypeConflict
)

var (
	newProblem     = httpapi.NewProblem
	notFound       = httpapi.NotFound
	invalidRequest = httpapi.InvalidRequest
)

func forbidden(detail string) error {
	return newProblem(http.StatusForbidden, httpapi.TypeForbidden, detail)
}
