// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package logring

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func logger(t *testing.T, size int, level slog.Level) (*slog.Logger, *Ring, *bytes.Buffer, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	ring := New(size, reg)
	var out bytes.Buffer
	return slog.New(ring.Handler(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: level}))), ring, &out, reg
}

func messages(records []Record) string {
	var out []string
	for _, r := range records {
		out = append(out, r.Message)
	}
	return strings.Join(out, " ")
}

func TestKeepsTheLastRecordsNewestFirst(t *testing.T) {
	log, ring, out, _ := logger(t, 3, slog.LevelInfo)
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		log.Info(m)
	}
	records, more := ring.Records(Filter{})
	if got := messages(records); got != "e d c" || more {
		t.Errorf("records = %q, more %v; want \"e d c\", false", got, more)
	}
	if records[0].ID != 5 || records[2].ID != 3 {
		t.Errorf("IDs = %d..%d, want 5..3", records[0].ID, records[2].ID)
	}
	if !strings.Contains(out.String(), "msg=a") {
		t.Errorf("the wrapped handler didn't get the records: %s", out)
	}
}

func TestFilters(t *testing.T) {
	log, ring, _, _ := logger(t, 10, slog.LevelDebug)
	log.Debug("quiet", "source", "xkcd")
	log.Info("indexed", "source", "xkcd")
	log.Warn("backing off", "source", "other")
	log.Error("stopped", "source", "xkcd", "read", 7)

	for _, tc := range []struct {
		name   string
		filter Filter
		want   string
		more   bool
	}{
		{"level", Filter{Level: slog.LevelWarn}, "stopped backing off", false},
		{"attr", Filter{Level: slog.LevelDebug, Attrs: map[string]string{"source": "xkcd"}}, "stopped indexed quiet", false},
		{"two attrs", Filter{Attrs: map[string]string{"source": "xkcd", "read": "7"}}, "stopped", false},
		{"limit", Filter{Limit: 2}, "stopped backing off", true},
		{"before", Filter{Level: slog.LevelDebug, Before: 3, Limit: 1}, "indexed", true},
		{"nothing", Filter{Attrs: map[string]string{"source": "none"}}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, more := ring.Records(tc.filter)
			if got := messages(records); got != tc.want || more != tc.more {
				t.Errorf("records = %q, more %v; want %q, %v", got, more, tc.want, tc.more)
			}
		})
	}
}

func TestKeepsOnlyWhatTheLevelLets(t *testing.T) {
	log, ring, _, _ := logger(t, 10, slog.LevelInfo)
	log.Debug("hidden")
	log.Info("shown")
	if records, _ := ring.Records(Filter{}); messages(records) != "shown" {
		t.Errorf("records = %q, want only shown", messages(records))
	}
}

func TestFlattensAttrsAndGroups(t *testing.T) {
	log, ring, _, _ := logger(t, 10, slog.LevelInfo)
	log.With("service", "server").WithGroup("req").With("id", 3).Info("hello",
		"error", errors.New("boom"), slog.Group("peer", "ip", "203.0.113.9"))
	records, _ := ring.Records(Filter{})
	want := map[string]string{"service": "server", "req.id": "3", "req.error": "boom", "req.peer.ip": "203.0.113.9"}
	for k, v := range want {
		if records[0].Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q (all: %v)", k, records[0].Attrs[k], v, records[0].Attrs)
		}
	}
	if len(records[0].Attrs) != len(want) {
		t.Errorf("attrs = %v, want %v", records[0].Attrs, want)
	}
}

func TestSizeZeroKeepsNothingButCounts(t *testing.T) {
	log, ring, out, reg := logger(t, 0, slog.LevelInfo)
	log.Error("boom")
	log.Info("fine")
	if records, more := ring.Records(Filter{}); len(records) != 0 || more {
		t.Errorf("records = %v, more %v; want none", records, more)
	}
	if !strings.Contains(out.String(), "boom") {
		t.Error("the wrapped handler didn't get the record")
	}
	if n, err := testutil.GatherAndCount(reg, "andante_log_records_total"); err != nil || n != 2 {
		t.Errorf("andante_log_records_total series = %d, %v; want 2", n, err)
	}
	if got := testutil.ToFloat64(ring.written.WithLabelValues("error")); got != 1 {
		t.Errorf("errors counted = %v, want 1", got)
	}
}
