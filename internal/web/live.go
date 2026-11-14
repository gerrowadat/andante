// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// What a page that shows something as it happens gets: a Server-Sent Events
// stream, whose events are the same template fragments the page is made of, for
// htmx to swap in.

const (
	// streamsEach is how many streams one person can have open on one server. A
	// browser over HTTP/1.1 only opens six connections to a site anyway.
	streamsEach = 8
	// firstListenRetry and lastListenRetry bound the wait after losing the
	// database connection that hears about changes.
	firstListenRetry = 250 * time.Millisecond
	lastListenRetry  = 30 * time.Second
)

// These are variables so that tests don't wait as long.
var (
	// heartbeat is how often a quiet stream sends a comment, which stops a proxy
	// deciding it's idle and closing it.
	heartbeat = 15 * time.Second
	// settle is how long a source stream waits after hearing of a change, so that a
	// crawl's burst of changes is shown once.
	settle = time.Second
)

type streamMetrics struct {
	open    *prometheus.GaugeVec
	events  *prometheus.CounterVec
	refused *prometheus.CounterVec
}

func newStreamMetrics(reg prometheus.Registerer) streamMetrics {
	factory := promauto.With(reg)
	return streamMetrics{
		open: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "andante_web_streams", Help: "Open event streams to the website's pages, by stream: discovery or source.",
		}, []string{"stream"}),
		events: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_web_stream_events_total", Help: "Events sent on the website's event streams, by stream.",
		}, []string{"stream"}),
		refused: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_web_streams_refused_total", Help: "Event streams refused because someone had too many open, by stream.",
		}, []string{"stream"}),
	}
}

// streamLimit counts each person's open streams.
type streamLimit struct {
	mu     sync.Mutex
	byUser map[int64]int
}

func (l *streamLimit) take(user int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byUser[user] >= streamsEach {
		return false
	}
	l.byUser[user]++
	return true
}

func (l *streamLimit) give(user int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byUser[user]--; l.byUser[user] <= 0 {
		delete(l.byUser, user)
	}
}

// eventStream is one open stream.
type eventStream struct {
	name string
	w    http.ResponseWriter
	rc   *http.ResponseController
	beat *time.Ticker
	sent prometheus.Counter
}

// openStream starts an event stream, or refuses it if this person has enough open.
// The caller closes it.
func (s *site) openStream(w http.ResponseWriter, r *http.Request, v visitor, name string) (*eventStream, bool) {
	if !s.streamLimit.take(v.actor.UserID) {
		s.streams.refused.WithLabelValues(name).Inc()
		http.Error(w, "You have too many pages open that update themselves. Close some, or reload this one.", http.StatusTooManyRequests)
		return nil, false
	}
	rc := http.NewResponseController(w)
	// The server's read timeout would end the request part way through, and its
	// write timeout would cut the stream off. Each write sets its own deadline.
	_ = rc.SetReadDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	// nginx holds a response back to buffer it unless it's told not to.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	e := &eventStream{name: name, w: w, rc: rc, beat: time.NewTicker(heartbeat), sent: s.streams.events.WithLabelValues(name)}
	s.streams.open.WithLabelValues(name).Inc()
	_ = e.write([]byte(": " + name + "\n\n"))
	return e, true
}

func (s *site) closeStream(e *eventStream, v visitor) {
	e.beat.Stop()
	s.streams.open.WithLabelValues(e.name).Dec()
	s.streamLimit.give(v.actor.UserID)
}

func (e *eventStream) write(b []byte) error {
	// Long enough to reach the next heartbeat and then some.
	_ = e.rc.SetWriteDeadline(time.Now().Add(2 * heartbeat))
	if _, err := e.w.Write(b); err != nil {
		return err
	}
	return e.rc.Flush()
}

// send sends an event. With no name it's HTML for htmx to swap in; a named one is
// a signal, such as the one that tells htmx to stop.
func (e *eventStream) send(event string, data []byte) error {
	var b bytes.Buffer
	if event != "" {
		b.WriteString("event: " + event + "\n")
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		b.WriteString("data: " + strings.TrimSuffix(line, "\r") + "\n")
	}
	b.WriteString("\n")
	if err := e.write(b.Bytes()); err != nil {
		return err
	}
	e.sent.Inc()
	return nil
}

// ping is the heartbeat: a comment, which the browser ignores.
func (e *eventStream) ping() error { return e.write([]byte(":\n\n")) }

// watcher hears from the database when the sync changes a source, and tells the
// streams showing it. It listens only while someone's watching, so a server nobody
// is looking at holds no connection for it.
type watcher struct {
	listen   func(ctx context.Context, ready func(), changed func(sourceID int64)) error
	logger   *slog.Logger
	failures prometheus.Counter

	mu     sync.Mutex
	bySrc  map[int64]map[chan struct{}]struct{}
	count  int
	cancel context.CancelFunc
}

func newWatcher(opts Options) *watcher {
	w := &watcher{
		logger: opts.Logger,
		failures: promauto.With(opts.Registry).NewCounter(prometheus.CounterOpts{
			Name: "andante_web_listen_failures_total",
			Help: "Times the website lost, or couldn't get, the database connection that hears about changed sources.",
		}),
		bySrc: map[int64]map[chan struct{}]struct{}{},
	}
	if opts.Store != nil {
		w.listen = opts.Store.ListenSources
	}
	return w
}

// watch returns a channel that has something on it when the source changes, and a
// function that stops watching.
func (w *watcher) watch(sourceID int64) (<-chan struct{}, func()) {
	changed := make(chan struct{}, 1)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.bySrc[sourceID] == nil {
		w.bySrc[sourceID] = map[chan struct{}]struct{}{}
	}
	w.bySrc[sourceID][changed] = struct{}{}
	if w.count++; w.cancel == nil {
		var ctx context.Context
		ctx, w.cancel = context.WithCancel(context.Background())
		go w.run(ctx)
	}
	return changed, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.bySrc[sourceID], changed)
		if len(w.bySrc[sourceID]) == 0 {
			delete(w.bySrc, sourceID)
		}
		if w.count--; w.count == 0 {
			w.cancel()
			w.cancel = nil
		}
	}
}

func nudge(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (w *watcher) changed(sourceID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.bySrc[sourceID] {
		nudge(ch)
	}
}

// all tells everyone to look again, after a time nobody was listening, when they
// could have missed something.
func (w *watcher) all() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, each := range w.bySrc {
		for ch := range each {
			nudge(ch)
		}
	}
}

func (w *watcher) run(ctx context.Context) {
	retry := firstListenRetry
	for {
		listening := false
		err := w.listen(ctx, func() { listening = true; w.all() }, w.changed)
		if ctx.Err() != nil {
			return
		}
		w.failures.Inc()
		w.logger.Warn("lost the connection that hears about changed sources", "error", err)
		if listening {
			retry = firstListenRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
		retry = min(retry*2, lastListenRetry)
	}
}
