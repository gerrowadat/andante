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

func TestAJobKeepsItsStages(t *testing.T) {
	j := newJobs()
	created, _ := j.start(1, "https://example.com/", time.Now())
	before, _ := j.get(created.id, 1)
	j.stage(created.id, "one")
	select {
	case <-before.changed:
	default:
		t.Fatal("a stage didn't wake a page waiting on the job")
	}
	after, _ := j.get(created.id, 1)
	if after.changed == before.changed {
		t.Fatal("the job's still on a closed channel")
	}
	j.stage(created.id, "two")
	if len(after.stages) != 1 {
		t.Errorf("a copy's stages changed under it: %v", after.stages)
	}
	if got := foundOf(after); len(got.Stages) != 0 || got.Current != "one" {
		t.Errorf("found = %+v", got)
	}
	for range stagesEach * 2 {
		j.stage(created.id, "more")
	}
	j.finish(created.id, nil, "")
	j.stage(created.id, "after the end")
	done, _ := j.get(created.id, 1)
	if len(done.stages) != stagesEach || done.stages[1] != "two" {
		t.Errorf("stages = %d, %v", len(done.stages), done.stages[:2])
	}
	if got := foundOf(done); len(got.Stages) != stagesEach || got.Current != "" {
		t.Errorf("a finished job shows %d stages and %q under way", len(got.Stages), got.Current)
	}
}
