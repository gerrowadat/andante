// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package logring keeps the last records a service logged, in memory, so that an admin
// can read them without access to wherever the service's output goes.
package logring

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Record is one logged line. Attrs are flattened, with a group's name and a dot in
// front of the keys inside it.
type Record struct {
	ID      int64
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   map[string]string
}

// Ring holds the last records. The zero size keeps none.
type Ring struct {
	mu      sync.Mutex
	records []Record
	// next is the ID of the next record. IDs start at 1 and go up by one, so a
	// record's place in records is its ID modulo the size.
	next    int64
	written *prometheus.CounterVec
}

// New returns a Ring that keeps size records, and counts what's logged in reg.
func New(size int, reg prometheus.Registerer) *Ring {
	return &Ring{
		records: make([]Record, max(size, 0)),
		next:    1,
		written: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_log_records_total",
			Help: "Records logged, by level.",
		}, []string{"level"}),
	}
}

// Handler wraps next, so that what goes to next is kept here too.
func (r *Ring) Handler(next slog.Handler) slog.Handler {
	return &handler{ring: r, next: next}
}

func (r *Ring) add(rec Record) {
	r.written.WithLabelValues(LevelName(rec.Level)).Inc()
	if len(r.records) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.ID = r.next
	r.records[rec.ID%int64(len(r.records))] = rec
	r.next++
}

// Filter picks records.
type Filter struct {
	// Level is the least severe level to include. The zero value is info.
	Level slog.Level
	// Attrs must all be present with these values.
	Attrs map[string]string
	// Before leaves out records from Before on. Zero leaves nothing out.
	Before int64
	// Limit is the most records to return. Zero is no limit.
	Limit int
}

func (f Filter) match(rec Record) bool {
	if rec.Level < f.Level {
		return false
	}
	for k, v := range f.Attrs {
		if rec.Attrs[k] != v {
			return false
		}
	}
	return true
}

// Records returns the records that match, newest first. more says whether there are
// older ones that match beyond the limit.
func (r *Ring) Records(f Filter) (records []Record, more bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := int64(len(r.records))
	newest := r.next - 1
	if f.Before > 0 {
		newest = min(newest, f.Before-1)
	}
	oldest := max(r.next-size, 1)
	for id := newest; id >= oldest; id-- {
		rec := r.records[id%size]
		if !f.match(rec) {
			continue
		}
		if f.Limit > 0 && len(records) == f.Limit {
			return records, true
		}
		records = append(records, rec)
	}
	return records, false
}

// LevelName names a level as the APIs do: debug, info, warn or error. A level
// between two takes the name of the one below it.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	}
	return "error"
}

// ParseLevel is the reverse of LevelName. Anything else is debug.
func ParseLevel(name string) slog.Level {
	switch name {
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelDebug
}

type handler struct {
	ring   *Ring
	next   slog.Handler
	attrs  []slog.Attr
	prefix string // The open groups, each followed by a dot.
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	kept := Record{Time: rec.Time.UTC(), Level: rec.Level, Message: rec.Message, Attrs: map[string]string{}}
	for _, a := range h.attrs {
		flatten(kept.Attrs, "", a)
	}
	rec.Attrs(func(a slog.Attr) bool {
		flatten(kept.Attrs, h.prefix, a)
		return true
	})
	h.ring.add(kept)
	return h.next.Handle(ctx, rec)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := *h
	out.next = h.next.WithAttrs(attrs)
	out.attrs = append([]slog.Attr{}, h.attrs...)
	for _, a := range attrs {
		out.attrs = append(out.attrs, slog.Attr{Key: h.prefix + a.Key, Value: a.Value})
	}
	return &out
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	out := *h
	out.next = h.next.WithGroup(name)
	out.prefix = h.prefix + name + "."
	return &out
}

// flatten adds an attr, and those inside a group under the group's name.
func flatten(into map[string]string, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, inner := range a.Value.Group() {
			flatten(into, prefix, inner)
		}
		return
	}
	if a.Key == "" {
		return
	}
	into[prefix+a.Key] = a.Value.String()
}
