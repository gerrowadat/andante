// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func heard(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("heard nothing")
	}
}

// The watcher listens only while someone watches, gets the connection back when it
// loses it, and has everyone look again then, since they could have missed something.
func TestTheWatcherListensWhileSomeoneWatches(t *testing.T) {
	var running, calls atomic.Int32
	announce := make(chan int64)
	w := newWatcher(Options{Logger: slog.New(slog.DiscardHandler), Registry: prometheus.NewRegistry()})
	w.listen = func(ctx context.Context, ready func(), changed func(int64)) error {
		running.Add(1)
		defer running.Add(-1)
		if calls.Add(1) == 1 {
			return errors.New("no database")
		}
		ready()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case id := <-announce:
				changed(id)
			}
		}
	}

	one, stopOne := w.watch(1)
	two, stopTwo := w.watch(2)
	// The first try fails, and the second has everyone look again.
	heard(t, one)
	heard(t, two)
	if got := testutil.ToFloat64(w.failures); got != 1 {
		t.Errorf("failures = %v", got)
	}

	announce <- 2
	heard(t, two)
	select {
	case <-one:
		t.Error("source 1 heard of a change to source 2")
	case <-time.After(50 * time.Millisecond):
	}

	stopOne()
	stopTwo()
	deadline := time.Now().Add(10 * time.Second)
	for running.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("still listening with nobody watching")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := testutil.ToFloat64(w.failures); got != 1 {
		t.Errorf("stopping counted as a failure: %v", got)
	}
}

func TestTheStreamLimitIsPerPerson(t *testing.T) {
	l := &streamLimit{byUser: map[int64]int{}}
	for range streamsEach {
		if !l.take(1) {
			t.Fatal("refused under the limit")
		}
	}
	if l.take(1) || !l.take(2) {
		t.Error("the limit isn't each person's")
	}
	l.give(1)
	if !l.take(1) {
		t.Error("giving one back didn't make room")
	}
	for range streamsEach + 1 {
		l.give(1)
	}
	l.give(2)
	if len(l.byUser) != 0 {
		t.Errorf("left over: %v", l.byUser)
	}
}
