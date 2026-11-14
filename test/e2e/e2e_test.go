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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/jackc/pgx/v5"
	"github.com/mmcdole/gofeed"
)

const version = "v0.0.0-e2e"

// crawlFlags let the catalogue crawl the loopback fixture sites, quickly.
var crawlFlags = []string{"--crawl-allow-private", "--crawl-interval=1ms", "--crawl-poll-interval=20ms"}

func build(t *testing.T, name string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	ldflags := "-X github.com/gerrowadat/andante/internal/buildinfo.version=" + version
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "../../cmd/"+name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func buildServer(t *testing.T) string { return build(t, "andante-server") }

func TestBinariesReportTheirVersion(t *testing.T) {
	for _, name := range []string{"andante-server", "andante-catalogue"} {
		bin := build(t, name)
		for _, arg := range []string{"--version", "version"} {
			out, err := exec.Command(bin, arg).CombinedOutput()
			if err != nil {
				t.Fatalf("%s %s: %v\n%s", name, arg, err, out)
			}
			if !strings.Contains(string(out), version) {
				t.Errorf("%s %s output %q does not contain %q", name, arg, out, version)
			}
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
	bin    string
	dsn    string
	admin  string
	public string
	// root is the path of --base-url, which every route is under.
	root   string
	mu     sync.Mutex
	logs   strings.Builder
	waited chan error
}

const (
	catalogueToken = "e2e-catalogue-token"
	secret         = "an-end-to-end-secret-of-enough-length"
)

// startServer starts andante-server. With no --catalogue-url in flags it points
// nowhere, which is fine for anything that doesn't touch sources.
func startServer(t *testing.T, bin, dsn string, flags ...string) *process {
	t.Helper()
	p := &process{admin: freeAddr(t), public: freeAddr(t), waited: make(chan error, 1)}
	return p.start(t, exec.Command(bin, append([]string{
		"--database-url=" + dsn,
		"--base-url=https://andante.example",
		"--listen=" + p.public,
		"--ops-listen=" + p.admin,
		"--log-format=text",
		"--catalogue-url=http://127.0.0.1:1/v1",
		"--catalogue-token=" + catalogueToken,
		"--secret=" + secret,
	}, flags...)...), bin, dsn)
}

// startCatalogue starts andante-catalogue. Its listener is in admin, because that's
// what waitReady looks at.
func startCatalogue(t *testing.T, bin, dsn string, flags ...string) *process {
	t.Helper()
	p := &process{admin: freeAddr(t), waited: make(chan error, 1)}
	return p.start(t, exec.Command(bin, append([]string{
		"--database-url=" + dsn,
		"--base-url=https://andante.example",
		"--listen=" + p.admin,
		"--api-token=" + catalogueToken,
		"--log-format=text",
	}, flags...)...), bin, dsn)
}

// startBoth starts a catalogue, with crawlFlags, and a server that uses it.
func startBoth(t *testing.T, flags ...string) *process {
	t.Helper()
	catalogue := startCatalogue(t, build(t, "andante-catalogue"), storetest.DSN(t), crawlFlags...)
	catalogue.waitReady(t)
	return startServer(t, buildServer(t), storetest.DSN(t), append([]string{catalogue.flag()}, flags...)...)
}

func (p *process) flag() string { return "--catalogue-url=http://" + p.admin + "/v1" }

// addUser runs andante-server user add, as an operator would, and returns the token.
func (p *process) addUser(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command(p.bin, "user", "add", name, "--admin", "--database-url="+p.dsn).Output()
	if err != nil {
		t.Fatalf("user add: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func (p *process) start(t *testing.T, cmd *exec.Cmd, bin, dsn string) *process {
	t.Helper()
	p.cmd, p.bin, p.dsn = cmd, bin, dsn
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
		if status(p.admin+p.root+"/readyz") == http.StatusOK {
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
	if applied != 7 {
		t.Errorf("recorded %d migrations, want 7", applied)
	}
}

func TestAPIThroughTheRealServer(t *testing.T) {
	site := sitetest.Serve(t, "comiccontrol")
	p := startBoth(t)
	p.waitReady(t)

	token := p.addUser(t, "admin")
	c, err := client.NewClientWithResponses("http://"+p.public+"/api/v1",
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
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

	if code := status(p.public + "/api/v1/sources"); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}
	if code := status(p.admin + "/api/v1/sources"); code != http.StatusNotFound {
		t.Errorf("ops listener: %d, want 404", code)
	}
	if code := status(p.public + "/metrics"); code != http.StatusNotFound {
		t.Errorf("metrics on the public listener: %d, want 404", code)
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

func TestFeedsThroughTheRealServer(t *testing.T) {
	site := sitetest.Serve(t, "comiccontrol")
	p := startBoth(t)
	p.waitReady(t)

	apiToken := p.addUser(t, "admin")
	c, err := client.NewClientWithResponses("http://"+p.public+"/api/v1",
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+apiToken)
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	var config client.SourceConfig
	if err := config.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: site.URL + "/1.html"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := c.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "e2e", Title: "E2E", HomeURL: site.URL, DefaultKind: client.Page, Config: config,
	})
	if err != nil || source.JSON201 == nil {
		t.Fatalf("create source: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := c.GetSourceWithResponse(ctx, source.JSON201.ID)
		if err == nil && got.JSON200 != nil && got.JSON200.State == client.SourceStateTail {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the source never reached tail mode. Logs:\n%s", p.output())
		}
		time.Sleep(20 * time.Millisecond)
	}

	var pacing client.Pacing
	if err := pacing.FromRRulePacing(client.RRulePacing{Mode: client.Rrule, Rrule: "FREQ=DAILY", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	read, err := c.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: source.JSON201.ID, Name: "r", Pacing: pacing})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, read.Body)
	}
	token, err := c.CreateFeedTokenWithResponse(ctx, read.JSON201.ID, client.FeedTokenCreate{Role: client.Viewer, Label: "e2e"})
	if err != nil || token.JSON201 == nil {
		t.Fatalf("create token: %v", err)
	}

	resp, err := http.Get("http://" + p.public + "/f/" + token.JSON201.Token + ".rss")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	parsed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Title != "Harbour Lights - Page 1" {
		t.Errorf("items = %+v", parsed.Items)
	}
	if strings.Contains(p.output(), token.JSON201.Token) {
		t.Error("a feed token reached the logs")
	}
}

func TestProbeOutlivesTheWriteTimeout(t *testing.T) {
	bin := buildServer(t)
	site := sitetest.Serve(t, "comiccontrol")
	// Five pages at this interval take far longer than the write timeout.
	catalogue := startCatalogue(t, build(t, "andante-catalogue"), storetest.DSN(t), "--crawl-allow-private", "--crawl-interval=100ms")
	catalogue.waitReady(t)
	p := startServer(t, bin, storetest.DSN(t), catalogue.flag(), "--write-timeout=200ms")
	p.waitReady(t)

	token := p.addUser(t, "admin")
	c, err := client.NewClientWithResponses("http://"+p.public+"/api/v1",
		client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
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

func TestRestartKeepsUsers(t *testing.T) {
	bin := buildServer(t)
	dsn := storetest.DSN(t)

	first := startServer(t, bin, dsn)
	first.waitReady(t)
	token := first.addUser(t, "dave")
	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-first.waited

	second := startServer(t, bin, dsn)
	second.waitReady(t)
	if strings.Contains(second.output(), "no users yet") {
		t.Errorf("the second start lost the user. Logs:\n%s", second.output())
	}
	st := storetest.Open(t, dsn)
	if _, ok, err := st.AuthenticateAPIToken(context.Background(), token); err != nil || !ok {
		t.Errorf("the token after a restart: %v, %v", ok, err)
	}
}

func TestTheWrongSecretStopsTheServer(t *testing.T) {
	bin := buildServer(t)
	dsn := storetest.DSN(t)
	first := startServer(t, bin, dsn)
	first.waitReady(t)
	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-first.waited

	second := startServer(t, bin, dsn, "--secret="+strings.Repeat("x", 32))
	if err := <-second.waited; err == nil {
		t.Fatal("the server started with the wrong secret")
	}
	if !strings.Contains(second.output(), "--secret is not the one") {
		t.Errorf("no clear message. Logs:\n%s", second.output())
	}
}
