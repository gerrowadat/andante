// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"testing"
	"time"
)

func TestJobsAreBoundedForEachPerson(t *testing.T) {
	j, now := newJobs(), time.Now()
	var first string
	for i := range keptEach * 5 {
		created, ok := j.start(1, "https://example.com/", now.Add(time.Duration(i)*time.Second))
		if !ok {
			t.Fatalf("job %d refused", i)
		}
		if i == 0 {
			first = created.id
		}
		j.finish(created.id, nil, "")
	}
	if len(j.byID) != keptEach {
		t.Errorf("kept %d answers, want %d", len(j.byID), keptEach)
	}
	if _, ok := j.get(first, 1); ok {
		t.Error("the oldest answer is still there")
	}

	for range jobsEach {
		if _, ok := j.start(2, "https://example.com/", now); !ok {
			t.Fatal("someone else was refused")
		}
	}
	if _, ok := j.start(2, "https://example.com/", now); ok {
		t.Error("a fourth look at once")
	}
	if len(j.byID) != keptEach+jobsEach {
		t.Errorf("jobs = %d", len(j.byID))
	}
}
