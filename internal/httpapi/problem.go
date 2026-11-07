// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Problem type URIs. Each resolves to the section of docs/api.md explaining it.
const (
	typeBase           = "https://github.com/gerrowadat/andante/blob/main/docs/api.md#"
	TypeInvalidRequest = typeBase + "invalid-request"
	TypeInvalidConfig  = typeBase + "invalid-config"
	TypeInvalidRead    = typeBase + "invalid-read"
	TypeConflict       = typeBase + "conflict"
	TypeProbeFailed    = typeBase + "probe-failed"
	TypeForbidden      = typeBase + "forbidden"
	TypeUnauthorized   = typeBase + "unauthorized"
	TypeCatalogueDown  = typeBase + "catalogue-unavailable"
	TypeBlank          = "about:blank"
)

// FieldError is one thing wrong with a request.
type FieldError struct {
	Pointer *string `json:"pointer,omitempty"`
	Detail  string  `json:"detail"`
}

// Problem is RFC 9457 problem details. Both services' API documents describe it.
type Problem struct {
	Type   string        `json:"type"`
	Title  string        `json:"title"`
	Status int32         `json:"status"`
	Detail *string       `json:"detail,omitempty"`
	Errors *[]FieldError `json:"errors,omitempty"`
	// ExistingSourceID says which source a duplicate would repeat.
	ExistingSourceID *int64 `json:"existing_source_id,omitempty"`
}

// Error is an error that renders as problem details.
type Error struct {
	Problem Problem
}

func (e *Error) Error() string { return e.Problem.Title }

// NewProblem returns an Error.
func NewProblem(status int, problemType, detail string) *Error {
	return &Error{Problem: Problem{
		Type:   problemType,
		Title:  http.StatusText(status),
		Status: int32(status), //nolint:gosec // HTTP status codes fit.
		Detail: &detail,
	}}
}

// NotFound is the error for something that doesn't exist.
func NotFound(what string, id int64) error {
	return NewProblem(http.StatusNotFound, TypeBlank, fmt.Sprintf("%s %d does not exist", what, id))
}

// InvalidRequest is the error for a request that can't be used.
func InvalidRequest(detail string) error {
	return NewProblem(http.StatusBadRequest, TypeInvalidRequest, detail)
}

// WriteProblem renders a problem.
func WriteProblem(w http.ResponseWriter, p Problem) {
	body, err := json.Marshal(p)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(int(p.Status))
	_, _ = w.Write(body)
}

// WriteError renders err, hiding anything that is not a deliberate problem.
func (k *Kit) WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var p *Error
	if errors.As(err, &p) {
		WriteProblem(w, p.Problem)
		return
	}
	k.Logger.Error("API request failed", "request_id", RequestID(r.Context()), "error", err)
	WriteProblem(w, Problem{
		Type:   TypeBlank,
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
	})
}
