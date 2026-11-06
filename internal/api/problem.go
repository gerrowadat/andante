// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gerrowadat/andante/pkg/client"
)

// Problem type URIs. Each resolves to the section of docs/api.md explaining it.
const (
	problemBase           = "https://github.com/gerrowadat/andante/blob/main/docs/api.md#"
	problemNotImplemented = problemBase + "not-implemented"
	problemInvalidRequest = problemBase + "invalid-request"
	problemInvalidConfig  = problemBase + "invalid-config"
	problemInvalidRead    = problemBase + "invalid-read"
	problemConflict       = problemBase + "conflict"
	problemProbeFailed    = problemBase + "probe-failed"
	problemUnauthorized   = problemBase + "unauthorized"
	problemBlank          = "about:blank"
)

// problemError is an error that renders as RFC 9457 problem details.
type problemError struct {
	problem client.Problem
}

func (e *problemError) Error() string { return e.problem.Title }

func newProblem(status int, problemType, detail string) *problemError {
	return &problemError{problem: client.Problem{
		Type:   problemType,
		Title:  http.StatusText(status),
		Status: int32(status), //nolint:gosec // HTTP status codes fit.
		Detail: &detail,
	}}
}

func notFound(what string, id int64) error {
	return newProblem(http.StatusNotFound, problemBlank, fmt.Sprintf("%s %d does not exist", what, id))
}

func notImplemented() error {
	return newProblem(http.StatusNotImplemented, problemNotImplemented, "this endpoint is described but not built yet")
}

func invalidRequest(detail string) error {
	return newProblem(http.StatusBadRequest, problemInvalidRequest, detail)
}

func writeProblem(w http.ResponseWriter, p client.Problem) {
	body, err := json.Marshal(p)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(int(p.Status))
	_, _ = w.Write(body)
}

// writeError renders err, hiding anything that is not a deliberate problem.
func (h *handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var p *problemError
	if errors.As(err, &p) {
		writeProblem(w, p.problem)
		return
	}
	h.logger.Error("API request failed", "request_id", requestID(r.Context()), "error", err)
	writeProblem(w, client.Problem{
		Type:   problemBlank,
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
	})
}
