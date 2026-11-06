// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

//go:build e2e

// Package e2e runs the real binaries as subprocesses. It is behind a build tag
// because it belongs to the release suite, not to fast CI.
package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/jackc/pgx/v5"
)

const version = "v0.0.0-e2e"

// crawlFlags let the server crawl the loopback fixture sites, quickly.
var crawlFlags = []string{"--crawl-allow-private", "--crawl-interval=1ms", "--crawl-poll-interval=20ms"}

func buildServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "andante-server")
	ldflags := "-X github.com/gerrowadat/andante/internal/buildinfo.version=" + version
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "../../cmd/andante-server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestServerReportsVersion(t *testing.T) {
	bin := buildServer(t)
	for _, arg := range []string{"--version", "version"} {
		out, err := exec.Command(bin, arg).CombinedOutput()
		if err != nil {
			t.Fatalf("andante-server %s: %v\n%s", arg, err, out)
		}
		if !strings.Contains(string(out), version) {
			t.Errorf("andante-server %s output %q does not contain %q", arg, out, version)
		}
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

type process struct {
	cmd    *exec.Cmd
	admin  string
	public string
	mu     sync.Mutex
	logs   strings.Builder
	waited chan error
}

func startServer(t *testing.T, bin, dsn string, flags ...string) *process {
	t.Helper()
	p := &process{admin: freeAddr(t), public: freeAddr(t), waited: make(chan error, 1)}
	p.cmd = exec.Command(bin, append([]string{
		"--database-url=" + dsn,
		"--base-url=https://andante.example",
		"--listen=" + p.public,
		"--admin-listen=" + p.admin,
		"--log-format=text",
	}, flags...)...)
	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			p.mu.Lock()
			fmt.Fprintln(&p.logs, scanner.Text())
			p.mu.Unlock()
		}
	}()
	go func() {
		<-drained
		p.waited <- p.cmd.Wait()
	}()
	t.Cleanup(func() { _ = p.cmd.Process.Kill() })
	return p
}

func (p *process) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.logs.String()
}

func (p *process) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if status(p.admin+"/readyz") == http.StatusOK {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become ready. Logs:\n%s", p.output())
}

func status(url string) int {
	resp, err := http.Get("http://" + url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestTwoServersShareOneDatabase(t *testing.T) {
	bin := buildServer(t)
	dsn := storetest.DSN(t)

	a, b := startServer(t, bin, dsn), startServer(t, bin, dsn)
	a.waitReady(t)
	b.waitReady(t)

	all := a.output() + b.output()
	if got := strings.Count(all, "shown only this once"); got != 1 {
		t.Errorf("bootstrap token logged %d times, want 1. Logs:\n%s", got, all)
	}
	if err := storetest.Open(t, dsn).Ready(context.Background()); err != nil {
		t.Errorf("Ready after both started: %v", err)
	}

	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var applied int
	// goose records one row per migration it applies, and its own table as 0.
	err = conn.QueryRow(context.Background(), "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("recorded %d migrations, want 1", applied)
	}
}

func TestAPIThroughTheRealServer(t *testing.T) {
	bin := buildServer(t)
	site := sitetest.Serve(t, "comiccontrol")
	p := startServer(t, bin, storetest.DSN(t), crawlFlags...)
	p.waitReady(t)

	match := regexp.MustCompile(`token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(p.output())
	if match == nil {
		t.Fatalf("no bootstrap token in the logs:\n%s", p.output())
	}
	c, err := client.NewClientWithResponses("http://"+p.admin+"/api/v1",
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+match[1])
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	var config client.SourceConfig
	if err := config.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: site.URL + "/1.html"}); err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateSourceWithResponse(context.Background(), client.SourceCreate{
		Slug: "e2e", Title: "E2E", HomeURL: site.URL, DefaultKind: client.Page, Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.JSON201 == nil {
		t.Fatalf("create source: %d %s", created.StatusCode(), created.Body)
	}

	list, err := c.ListSourcesWithResponse(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.JSON200 == nil || len(list.JSON200.Items) != 1 {
		t.Fatalf("list sources: %d %s", list.StatusCode(), list.Body)
	}

	// The crawl loop indexes the source on its own, then settles at the live end.
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := c.GetSourceWithResponse(context.Background(), created.JSON201.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.JSON200 != nil && got.JSON200.State == client.SourceStateTail {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the source never reached tail mode: %s\nLogs:\n%s", got.Body, p.output())
		}
		time.Sleep(50 * time.Millisecond)
	}
	entries, err := c.ListEntriesWithResponse(context.Background(), created.JSON201.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if entries.JSON200 == nil || len(entries.JSON200.Items) != 5 {
		t.Fatalf("entries: %d %s", entries.StatusCode(), entries.Body)
	}

	if code := status(p.admin + "/api/v1/sources"); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}
	if code := status(p.public + "/api/v1/sources"); code != http.StatusNotFound {
		t.Errorf("public listener: %d, want 404", code)
	}
	body, err := http.Get("http://" + p.admin + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Body.Close()
	metrics, err := io.ReadAll(body.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metrics), `andante_api_requests_total{route="GET /api/v1/sources",status="200"}`) {
		t.Errorf("no API metric in /metrics:\n%s", metrics)
	}
}

func TestProbeOutlivesTheWriteTimeout(t *testing.T) {
	bin := buildServer(t)
	site := sitetest.Serve(t, "comiccontrol")
	// Five pages at this interval take far longer than the write timeout.
	p := startServer(t, bin, storetest.DSN(t), "--crawl-allow-private", "--crawl-interval=100ms", "--write-timeout=200ms")
	p.waitReady(t)

	match := regexp.MustCompile(`token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(p.output())
	if match == nil {
		t.Fatalf("no bootstrap token in the logs:\n%s", p.output())
	}
	c, err := client.NewClientWithResponses("http://"+p.admin+"/api/v1",
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+match[1])
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	var config client.SourceConfig
	if err := config.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: site.URL + "/1.html"}); err != nil {
		t.Fatal(err)
	}
	resp, err := c.CreateProbeWithResponse(context.Background(), client.ProbeRequest{Config: config})
	if err != nil {
		t.Fatalf("the response was cut off: %v", err)
	}
	if resp.JSON200 == nil || len(resp.JSON200.Entries) != 5 {
		t.Fatalf("probe: %d %s", resp.StatusCode(), resp.Body)
	}
}

func TestSIGTERMShutsDownCleanly(t *testing.T) {
	bin := buildServer(t)
	p := startServer(t, bin, storetest.DSN(t))
	p.waitReady(t)

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.waited:
		if err != nil {
			t.Errorf("server exited with %v. Logs:\n%s", err, p.output())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("server did not exit. Logs:\n%s", p.output())
	}
}

func TestRestartKeepsTheBootstrapToken(t *testing.T) {
	bin := buildServer(t)
	dsn := storetest.DSN(t)

	first := startServer(t, bin, dsn)
	first.waitReady(t)
	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-first.waited

	second := startServer(t, bin, dsn)
	second.waitReady(t)
	if strings.Contains(second.output(), "shown only this once") {
		t.Errorf("a restart generated another token. Logs:\n%s", second.output())
	}
}
