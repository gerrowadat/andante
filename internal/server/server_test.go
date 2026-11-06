// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

func testConfig() config.Config {
	return config.Config{
		Listen:            "127.0.0.1:0",
		AdminListen:       "127.0.0.1:0",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		ShutdownTimeout:   5 * time.Second,
		ReadinessTimeout:  time.Second,
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// start runs a server and returns it, with a function that stops it and returns
// what Run returned.
func start(t *testing.T, cfg config.Config, ready ReadyFunc, logs io.Writer) (*Server, func() error) {
	t.Helper()
	s, err := New(cfg, Deps{Ready: ready, Registry: prometheus.NewRegistry(), Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	var once sync.Once
	var result error
	stop := func() error {
		once.Do(func() {
			cancel()
			result = <-done
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return s, stop
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func alwaysReady(context.Context) error { return nil }

func TestHealthz(t *testing.T) {
	s, _ := start(t, testConfig(), func(context.Context) error { return errors.New("down") }, io.Discard)
	if code, _ := get(t, "http://"+s.AdminAddr().String()+"/healthz"); code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 even when not ready", code)
	}
}

func TestReadyz(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		s, _ := start(t, testConfig(), alwaysReady, io.Discard)
		if code, _ := get(t, "http://"+s.AdminAddr().String()+"/readyz"); code != http.StatusOK {
			t.Errorf("/readyz = %d, want 200", code)
		}
	})
	t.Run("not ready", func(t *testing.T) {
		s, _ := start(t, testConfig(), func(context.Context) error { return errors.New("secret detail") }, io.Discard)
		code, body := get(t, "http://"+s.AdminAddr().String()+"/readyz")
		if code != http.StatusServiceUnavailable {
			t.Errorf("/readyz = %d, want 503", code)
		}
		if strings.Contains(body, "secret detail") {
			t.Errorf("body %q leaks the error", body)
		}
	})
	t.Run("hung check times out", func(t *testing.T) {
		cfg := testConfig()
		cfg.ReadinessTimeout = 10 * time.Millisecond
		s, _ := start(t, cfg, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, io.Discard)
		if code, _ := get(t, "http://"+s.AdminAddr().String()+"/readyz"); code != http.StatusServiceUnavailable {
			t.Errorf("/readyz = %d, want 503", code)
		}
	})
}

func TestMetrics(t *testing.T) {
	s, _ := start(t, testConfig(), alwaysReady, io.Discard)
	code, body := get(t, "http://"+s.AdminAddr().String()+"/metrics")
	if code != http.StatusOK {
		t.Fatalf("/metrics = %d, want 200", code)
	}
	for _, want := range []string{
		`andante_build_info{commit="` + buildinfo.Commit() + `",version="` + buildinfo.Version() + `"} 1`,
		"go_goroutines",
		"process_cpu_seconds_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics does not contain %q", want)
		}
	}
}

func TestAPIIsOnlyOnTheAdminListener(t *testing.T) {
	cfg := testConfig()
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	s, err := New(cfg, Deps{Ready: alwaysReady, Registry: prometheus.NewRegistry(), Logger: slog.New(slog.DiscardHandler), API: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	if code, _ := get(t, "http://"+s.AdminAddr().String()+"/api/v1/sources"); code != http.StatusTeapot {
		t.Errorf("admin /api/v1/sources = %d, want the API", code)
	}
	if code, _ := get(t, "http://"+s.PublicAddr().String()+"/api/v1/sources"); code != http.StatusNotFound {
		t.Errorf("public /api/v1/sources = %d, want 404", code)
	}
}

func TestHandlersCanOutliveTheWriteTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.WriteTimeout = 20 * time.Millisecond
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Errorf("SetWriteDeadline: %v", err)
		}
		time.Sleep(60 * time.Millisecond)
		_, _ = fmt.Fprint(w, "finished")
	})
	s, err := New(cfg, Deps{Ready: alwaysReady, Registry: prometheus.NewRegistry(), Logger: slog.New(slog.DiscardHandler), API: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	if code, body := get(t, "http://"+s.AdminAddr().String()+"/api/v1/probes"); code != http.StatusOK || body != "finished" {
		t.Errorf("got %d %q", code, body)
	}
}

func TestPublicListenerServesNothing(t *testing.T) {
	s, _ := start(t, testConfig(), alwaysReady, io.Discard)
	for _, path := range []string{"/", "/healthz", "/readyz", "/metrics", "/api/v1/sources", "/f/token.atom"} {
		if code, _ := get(t, "http://"+s.PublicAddr().String()+path); code != http.StatusNotFound {
			t.Errorf("public %s = %d, want 404", path, code)
		}
	}
}

func TestLogsNeverContainThePath(t *testing.T) {
	var logs lockedBuffer
	s, stop := start(t, testConfig(), alwaysReady, &logs)
	get(t, "http://"+s.PublicAddr().String()+"/f/secret-feed-token.atom")
	get(t, "http://"+s.AdminAddr().String()+"/healthz")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "secret-feed-token") {
		t.Errorf("logs contain a request path:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "route=\"GET /healthz\"") {
		t.Errorf("logs do not contain the route:\n%s", logs.String())
	}
}

func TestRecoverPanics(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := recoverPanics(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("logs do not mention the panic:\n%s", logs.String())
	}
}

func TestRecoverPanicsLeavesAbortHandlerAlone(t *testing.T) {
	h := recoverPanics(slog.New(slog.DiscardHandler), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestShutdownFinishesInFlightRequests(t *testing.T) {
	cfg := testConfig()
	cfg.ReadinessTimeout = 5 * time.Second
	entered, release := make(chan struct{}), make(chan struct{})
	s, stop := start(t, cfg, func(context.Context) error {
		close(entered)
		<-release
		return nil
	}, io.Discard)

	result := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + s.AdminAddr().String() + "/readyz")
		if err != nil {
			t.Error(err)
			result <- 0
			return
		}
		defer resp.Body.Close()
		result <- resp.StatusCode
	}()
	<-entered

	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	select {
	case err := <-stopped:
		t.Fatalf("Run returned %v with a request in flight", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if code := <-result; code != http.StatusOK {
		t.Errorf("in-flight request = %d, want 200", code)
	}
	if err := <-stopped; err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

func TestShutdownTimesOut(t *testing.T) {
	cfg := testConfig()
	cfg.ShutdownTimeout = 10 * time.Millisecond
	cfg.ReadinessTimeout = 5 * time.Second
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s, stop := start(t, cfg, func(context.Context) error {
		close(entered)
		<-release
		return nil
	}, io.Discard)
	go func() {
		if resp, err := http.Get("http://" + s.AdminAddr().String() + "/readyz"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	if err := stop(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v, want a deadline error", err)
	}
}

func TestNewFailsWhenAListenerCannotBind(t *testing.T) {
	s, _ := start(t, testConfig(), alwaysReady, io.Discard)
	taken := s.PublicAddr().String()
	for name, mutate := range map[string]func(*config.Config){
		"public": func(c *config.Config) { c.Listen = taken },
		"admin":  func(c *config.Config) { c.AdminListen = taken },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			mutate(&cfg)
			deps := Deps{Ready: alwaysReady, Registry: prometheus.NewRegistry(), Logger: slog.New(slog.DiscardHandler)}
			if _, err := New(cfg, deps); err == nil {
				t.Error("New succeeded, want an error")
			}
		})
	}
}
