// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gerrowadat/andante/internal/httpapi"
)

func TestAReasonIsNotAStatus(t *testing.T) {
	detail, empty, pointer := "https://example.com/ returned 404. Check it.", "", "/title"
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"an error":  {errors.New("no free slug for this title"), "no free slug for this title"},
		"a problem": {httpapi.NewProblem(422, httpapi.TypeProbeFailed, detail), detail},
		"a wrapped problem": {
			fmt.Errorf("create: %w", httpapi.NewProblem(422, httpapi.TypeProbeFailed, detail)), detail,
		},
		"field errors": {&httpapi.Error{Problem: httpapi.Problem{
			Title: "Bad Request", Status: 400, Detail: &detail,
			Errors: &[]httpapi.FieldError{{Pointer: &pointer, Detail: "title is too long"}},
		}}, detail + "; title is too long"},
		"only a title": {&httpapi.Error{Problem: httpapi.Problem{Title: "Unprocessable Entity", Status: 422, Detail: &empty}}, "Unprocessable Entity"},
	} {
		if got := reason(tc.err); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if got := didntWork(errors.New("no")); got != "That didn't work: no" {
		t.Errorf("didntWork: %q", got)
	}
}
