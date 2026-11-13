// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	specfile "github.com/gerrowadat/andante/api"
	"github.com/gerrowadat/andante/internal/buildinfo"
	cstore "github.com/gerrowadat/andante/internal/catalogue/store"
	catalogue "github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/spf13/cobra"
)

var update = flag.Bool("update", false, "rewrite the golden files")

type harness struct {
	url, token string
	store      *store.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	stack := stacktest.New(t)
	st := stack.Store
	_, token := storetest.User(t, st, "admin", true)
	return &harness{url: stack.URL, token: token, store: st}
}

type result struct {
	stdout, stderr string
	err            error
}

func run(args ...string) result {
	var stdout, stderr bytes.Buffer
	root := newRootCmd(&stdout, &stderr)
	root.SetArgs(args)
	err := root.Execute()
	return result{stdout.String(), stderr.String(), err}
}

func (h *harness) run(args ...string) result {
	return run(append(args, "--url", h.url, "--token", h.token)...)
}

func (h *harness) ok(t *testing.T, args ...string) string {
	t.Helper()
	r := h.run(args...)
	if r.err != nil {
		t.Fatalf("andante %s: %v\n%s", strings.Join(args, " "), r.err, r.stderr)
	}
	return r.stdout
}

func (h *harness) comic(t *testing.T) {
	t.Helper()
	h.ok(t, "source", "add", "https://comic.example/1", "--slug", "comic", "--title", "A Comic")
	for i := range 5 {
		_, err := h.store.Pool().Exec(t.Context(),
			"INSERT INTO entries (id, source_id, seq, kind, url, title, original_date, discovered_at, available_at) VALUES ((SELECT coalesce(max(id), 0) + 1 FROM entries), 1, $1, 'page', $2, $3, '2010-01-01', now() - interval '1 hour', now() - interval '1 hour')",
			i, "https://comic.example/"+strconv.Itoa(i), "Page "+strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
	}
}

var timestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})`)

var feedURL = regexp.MustCompile(`/f/[A-Za-z0-9_-]{43}`)

func golden(t *testing.T, name, got string) {
	t.Helper()
	got = timestamp.ReplaceAllString(got, "<time>")
	got = feedURL.ReplaceAllString(got, "/f/<token>")
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs:\n%s", name, got)
	}
}

func TestEveryOperationHasACommand(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(specfile.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if id, ok := cmd.Annotations[operationKey]; ok {
			covered[id] = true
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(newRootCmd(io.Discard, io.Discard))

	operations := map[string]bool{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			operations[op.OperationID] = true
			if !covered[op.OperationID] {
				t.Errorf("no command for %s", op.OperationID)
			}
		}
	}
	for id := range covered {
		if !operations[id] {
			t.Errorf("a command names %s, which is not in the spec", id)
		}
	}
}

func TestTheCLIOnlyUsesTheAPI(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for dep := range strings.SplitSeq(string(out), "\n") {
		internal := strings.HasPrefix(dep, "github.com/gerrowadat/andante/internal/")
		if internal && dep != "github.com/gerrowadat/andante/internal/buildinfo" {
			t.Errorf("the CLI imports %s", dep)
		}
	}
}

func TestVersion(t *testing.T) {
	t.Run("the flag", func(t *testing.T) {
		r := run("--version")
		if r.err != nil || strings.TrimSpace(r.stdout) != buildinfo.String() {
			t.Errorf("%+v", r)
		}
	})
	t.Run("no server", func(t *testing.T) {
		r := run("version", "--url", "http://127.0.0.1:1")
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !strings.Contains(r.stdout, "client: "+buildinfo.String()) || !strings.Contains(r.stdout, "server: not reachable") {
			t.Errorf("stdout = %q", r.stdout)
		}
	})
	t.Run("no server, as JSON", func(t *testing.T) {
		if r := run("version", "-o", "json", "--url", "http://127.0.0.1:1"); r.err == nil {
			t.Error("succeeded with nothing to print")
		}
	})
	t.Run("with a server", func(t *testing.T) {
		h := newHarness(t)
		got := h.ok(t, "version")
		if !strings.Contains(got, "server: "+buildinfo.Version()) {
			t.Errorf("stdout = %q", got)
		}
	})
}

func TestSourceCommands(t *testing.T) {
	h := newHarness(t)
	h.comic(t)

	golden(t, "source-list.txt", h.ok(t, "source", "list"))
	golden(t, "source-entries.txt", h.ok(t, "source", "entries", "comic"))
	golden(t, "source-get.txt", h.ok(t, "source", "get", "comic"))
	if byID, bySlug := h.ok(t, "source", "get", "1"), h.ok(t, "source", "get", "comic"); byID != bySlug {
		t.Errorf("an ID and a slug differ:\n%s\n%s", byID, bySlug)
	}

	edited := h.ok(t, "source", "edit", "comic", "--title", "Better", "--pause", "--image-url", "https://comic.example/i.png")
	if !strings.Contains(edited, "Better") || !strings.Contains(edited, "paused") {
		t.Errorf("edited = %s", edited)
	}
	h.ok(t, "source", "edit", "comic", "--clear-image", "--resume", "--kind", "article", "--home-url", "https://comic.example/home")
	h.ok(t, "source", "edit", "comic", "--selector", "next=a.next", "--start-url", "https://comic.example/2")
	var config []byte
	if err := h.store.Pool().QueryRow(t.Context(), "SELECT config FROM sources").Scan(&config); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "a.next") || !strings.Contains(string(config), "comic.example/2") {
		t.Errorf("config = %s", config)
	}

	if got := h.ok(t, "source", "crawl", "comic"); !strings.Contains(got, "crawl requested") {
		t.Errorf("crawl = %s", got)
	}
	if got := h.ok(t, "source", "rm", "comic"); !strings.Contains(got, "deleted") {
		t.Errorf("rm = %s", got)
	}
	if r := h.run("source", "get", "comic"); r.err == nil || !strings.Contains(r.err.Error(), "not found") {
		t.Errorf("get after rm: %v", r.err)
	}
}

func TestPaging(t *testing.T) {
	h := newHarness(t)
	for _, slug := range []string{"a", "b", "c"} {
		h.ok(t, "source", "add", "https://comic.example/"+slug, "--slug", slug)
	}
	first := h.run("source", "list", "--limit", "2")
	if strings.Contains(first.stdout, "  c  ") || !strings.Contains(first.stderr, "more: --cursor ") {
		t.Fatalf("%+v", first)
	}
	cursor := strings.TrimSpace(strings.TrimPrefix(first.stderr, "more: --cursor "))
	second := h.ok(t, "source", "list", "--limit", "2", "--cursor", cursor)
	if !strings.Contains(second, "  c  ") {
		t.Errorf("second page = %s", second)
	}
	// A slug beyond the first page still resolves.
	for i := range 55 {
		h.ok(t, "source", "add", "https://comic.example/more/"+strconv.Itoa(i), "--slug", "more-"+strconv.Itoa(i))
	}
	if got := h.ok(t, "source", "get", "more-54"); !strings.Contains(got, "more-54") {
		t.Errorf("get = %s", got)
	}
}

func TestProbe(t *testing.T) {
	h := newHarness(t)
	site := sitetest.Serve(t, "wordpress-webcomic")
	got := h.ok(t, "source", "probe", site.URL+"/comic/1/", "--limit", "5")
	got = strings.ReplaceAll(got, site.URL, "http://site")
	golden(t, "source-probe.txt", got)

	if out := h.ok(t, "source", "probe", site.URL+"/comic/1/", "--limit", "1", "--selector", "title=title", "--kind", "page"); !strings.Contains(out, "Tidewater") {
		t.Errorf("selector ignored: %s", out)
	}

	refused := sitetest.Serve(t, "robots-disallow")
	r := h.run("source", "probe", refused.URL+"/comics/1.html")
	if r.err == nil || !strings.Contains(r.err.Error(), "robots.txt") {
		t.Errorf("a refused probe: %v", r.err)
	}
}

func TestStrategiesFromTheCommandLine(t *testing.T) {
	h := newHarness(t)
	podcast := sitetest.Serve(t, "podcast")
	if got := h.ok(t, "source", "probe", podcast.URL+"/feed.xml", "--strategy", "feed", "--kind", "article", "--limit", "2"); !strings.Contains(got, "Episode 1") {
		t.Errorf("feed probe: %s", got)
	}
	numeric := sitetest.Serve(t, "numeric-json")
	got := h.ok(t, "source", "probe", numeric.URL+"/{n}/info.0.json", "--strategy", "numeric", "--start-id", "1", "--format", "json",
		"--field", "title=safe_title", "--field", "image=img", "--field", "year=year", "--field", "month=month", "--field", "day=day", "--limit", "2")
	if !strings.Contains(got, "Barrel") {
		t.Errorf("numeric probe: %s", got)
	}

	h.ok(t, "source", "add", podcast.URL+"/feed.xml", "--slug", "pod", "--strategy", "feed", "--kind", "episode", "--fill-gaps")
	h.ok(t, "source", "add", podcast.URL+"/feed.xml", "--slug", "paged", "--strategy", "feed-paged", "--max-pages", "3", "--page-param", "p", "--full-text", "--fill-gaps")
	h.ok(t, "source", "add", numeric.URL+"/{n}/info.0.json", "--slug", "num", "--strategy", "numeric", "--max-misses", "2", "--format", "json", "--field", "title=title", "--field", "date=d", "--field", "alt_text=alt")
	h.ok(t, "source", "add", podcast.URL+"/feed.xml", "--slug", "text", "--kind", "article", "--full-text")
	for _, slug := range []string{"pod", "paged", "num"} {
		h.ok(t, "source", "edit", slug, "--start-url", "https://new.example/{n}")
	}
	if r := h.run("source", "edit", "pod", "--selector", "next=a"); r.err == nil {
		t.Error("accepted --selector on a feed")
	}
	if r := h.run("source", "probe", "https://a.example/", "--strategy", "guess"); r.err == nil {
		t.Error("accepted a bad strategy")
	}
	if r := h.run("source", "probe", "https://a.example/{n}", "--strategy", "numeric", "--field", "colour=red"); r.err == nil {
		t.Error("accepted a bad field")
	}
	if r := h.run("source", "probe", "https://a.example/{n}", "--strategy", "numeric", "--field", "nonsense"); r.err == nil {
		t.Error("accepted a malformed field")
	}
}

func TestReadCommands(t *testing.T) {
	h := newHarness(t)
	h.comic(t)
	created := h.ok(t, "read", "create", "comic", "--daily", "07:00", "--tz", "Europe/Dublin", "--name", "Mornings")
	if !strings.Contains(created, "Mornings") {
		t.Fatalf("created = %s", created)
	}
	golden(t, "read-get.txt", h.ok(t, "read", "get", "1"))
	golden(t, "read-list.txt", h.ok(t, "read", "list"))

	if got := h.ok(t, "read", "skip", "1"); !strings.Contains(got, "Position:      1") {
		t.Errorf("skip = %s", got)
	}
	if got := h.ok(t, "read", "skip", "1", "2"); !strings.Contains(got, "Position:      3") {
		t.Errorf("skip 2 = %s", got)
	}
	h.ok(t, "read", "pause", "1")
	h.ok(t, "read", "resume", "1")
	if got := h.ok(t, "read", "jump", "1", "4"); !strings.Contains(got, "Position:      4") {
		t.Errorf("jump = %s", got)
	}
	h.ok(t, "read", "pace", "1", "--replay", "--speed", "7", "--max-gap", "P3D")
	golden(t, "read-history.txt", h.ok(t, "read", "history", "1"))

	if got := h.ok(t, "read", "edit", "1", "--name", "Evenings", "--config", `{"window":5}`); !strings.Contains(got, "Evenings") {
		t.Errorf("edit = %s", got)
	}
	if r := h.run("read", "edit", "1", "--config", "nope"); r.err == nil {
		t.Error("accepted bad JSON")
	}
	if got := h.ok(t, "read", "rm", "1"); !strings.Contains(got, "deleted") {
		t.Errorf("rm = %s", got)
	}
}

func TestReadStart(t *testing.T) {
	h := newHarness(t)
	h.comic(t)
	for _, tt := range []struct {
		start string
		want  string
	}{
		{"first", "Position:      0"},
		{"3", "Position:      3"},
		{"https://comic.example/2", "Position:      2"},
	} {
		if got := h.ok(t, "read", "create", "1", "--daily", "07:00", "--start", tt.start); !strings.Contains(got, tt.want) {
			t.Errorf("--start %s: %s", tt.start, got)
		}
	}
	if r := h.run("read", "create", "1", "--daily", "07:00", "--start", "https://comic.example/missing"); r.err == nil {
		t.Error("accepted a URL with no entry")
	}
	if got := h.ok(t, "read", "create", "1", "--daily", "07:00", "--initial", "3", "--catch-up", "buffer", "--buffer", "1"); !strings.Contains(got, "Position:      2") {
		t.Errorf("--initial: %s", got)
	}
}

func TestShorthandPacing(t *testing.T) {
	tests := []struct {
		name  string
		flags pacingFlags
		want  string
	}{
		{"daily", pacingFlags{daily: "07:00", timezone: "UTC", speed: 1},
			`{"mode":"rrule","rrule":"FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0","timezone":"UTC"}`},
		{"daily, late", pacingFlags{daily: "23:59", timezone: "Europe/Dublin", speed: 1},
			`{"mode":"rrule","rrule":"FREQ=DAILY;BYHOUR=23;BYMINUTE=59;BYSECOND=0","timezone":"Europe/Dublin"}`},
		{"weekdays with a count", pacingFlags{weekdays: "7:30", count: 3, timezone: "UTC", speed: 1},
			`{"mode":"rrule","per_release":3,"rrule":"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7;BYMINUTE=30;BYSECOND=0","timezone":"UTC"}`},
		{"days", pacingFlags{days: "mo,we,FR", at: "08:00", timezone: "UTC", speed: 1},
			`{"mode":"rrule","rrule":"FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=8;BYMINUTE=0;BYSECOND=0","timezone":"UTC"}`},
		{"a rule passed through", pacingFlags{rrule: "FREQ=WEEKLY;BYDAY=FR", count: 2, timezone: "UTC", speed: 1},
			`{"mode":"rrule","per_release":2,"rrule":"FREQ=WEEKLY;BYDAY=FR","timezone":"UTC"}`},
		{"replay", pacingFlags{replay: true, speed: 7},
			`{"mode":"replay","speed":7}`},
		{"replay with gaps", pacingFlags{replay: true, speed: 2.5, minGap: "PT12H", maxGap: "P3D", defaultGap: "P1D"},
			`{"default_gap":"P1D","max_gap":"P3D","min_gap":"PT12H","mode":"replay","speed":2.5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := tt.flags.pacing()
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatal(err)
			}
			normal, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if string(normal) != tt.want {
				t.Errorf("got  %s\nwant %s", normal, tt.want)
			}
		})
	}
}

func TestBadPacingFlags(t *testing.T) {
	tests := map[string]pacingFlags{
		"none":                 {speed: 1},
		"two":                  {daily: "07:00", rrule: "FREQ=DAILY", speed: 1},
		"a bad time":           {daily: "25:00", speed: 1},
		"not a time":           {weekdays: "morning", speed: 1},
		"days with no time":    {days: "MO", speed: 1},
		"days with a bad time": {days: "MO", at: "7", speed: 1},
		"speed on a schedule":  {daily: "07:00", speed: 2},
		"gap on a schedule":    {daily: "07:00", speed: 1, minGap: "P1D"},
		"a bad min gap":        {replay: true, speed: 1, minGap: "soon"},
		"a bad max gap":        {replay: true, speed: 1, maxGap: "soon"},
		"a bad default gap":    {replay: true, speed: 1, defaultGap: "soon"},
	}
	for name, flags := range tests {
		if _, err := flags.pacing(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTokenCommands(t *testing.T) {
	h := newHarness(t)
	h.comic(t)
	h.ok(t, "read", "create", "comic", "--daily", "07:00")

	issued := h.ok(t, "token", "add", "1", "--label", "Alex")
	if !strings.Contains(issued, "https://andante.example/f/") || !strings.Contains(issued, "viewer") {
		t.Errorf("issued = %s", issued)
	}
	if got := h.ok(t, "token", "add", "1", "--label", "Me", "--owner"); !strings.Contains(got, "owner") {
		t.Errorf("owner = %s", got)
	}
	golden(t, "token-list.txt", h.ok(t, "token", "list", "1"))
	if got := h.ok(t, "token", "rm", "1"); !strings.Contains(got, "revoked") {
		t.Errorf("rm = %s", got)
	}
}

func TestOPML(t *testing.T) {
	h := newHarness(t)
	h.comic(t)
	h.ok(t, "read", "create", "comic", "--daily", "07:00")

	got := h.ok(t, "export", "opml", "--issue", "viewer", "--label", "For Alex")
	if !strings.Contains(got, "<opml") || !strings.Contains(got, "xmlUrl=") {
		t.Errorf("export = %s", got)
	}
	if r := h.run("export", "opml", "--issue", "nobody", "--label", "x"); r.err == nil {
		t.Error("accepted a bad role")
	}

	file := filepath.Join(t.TempDir(), "feeds.opml")
	if err := os.WriteFile(file, []byte(got), 0o600); err != nil {
		t.Fatal(err)
	}
	imported := h.ok(t, "import", "opml", file)
	if !strings.Contains(imported, "created") || !strings.Contains(imported, "https://andante.example/f/") {
		t.Errorf("import: %s", imported)
	}
	if added := h.ok(t, "source", "list"); !strings.Contains(added, "paused") {
		t.Errorf("imported sources: %s", added)
	}
	if r := h.run("import", "opml", filepath.Join(t.TempDir(), "missing")); r.err == nil {
		t.Error("imported a missing file")
	}
}

func TestAPITokenCommands(t *testing.T) {
	h := newHarness(t)
	issued := h.ok(t, "api-token", "add", "--label", "laptop")
	if !strings.Contains(issued, "Token:") {
		t.Errorf("issued = %s", issued)
	}
	golden(t, "api-token-list.txt", h.ok(t, "api-token", "list"))
	if got := h.ok(t, "api-token", "rm", "2"); !strings.Contains(got, "revoked") {
		t.Errorf("rm = %s", got)
	}
}

func TestSpec(t *testing.T) {
	h := newHarness(t)
	var doc map[string]any
	if err := json.Unmarshal([]byte(h.ok(t, "api", "spec")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
}

func (h *harness) raw(t *testing.T, path string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.url+"/api/v1"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestJSONOutputIsTheAPIResponse(t *testing.T) {
	h := newHarness(t)
	h.comic(t)
	h.ok(t, "read", "create", "comic", "--daily", "07:00")
	h.ok(t, "token", "add", "1", "--label", "Alex")
	tests := []struct {
		path string
		args []string
	}{
		{"/sources", []string{"source", "list"}},
		{"/sources/1", []string{"source", "get", "1"}},
		{"/sources/1/entries", []string{"source", "entries", "1"}},
		{"/reads/1/segments", []string{"read", "history", "1"}},
		{"/reads/1/tokens", []string{"token", "list", "1"}},
		{"/version", []string{"version"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := h.ok(t, append(tt.args, "-o", "json")...)
			if want := h.raw(t, tt.path); got != want {
				t.Errorf("not byte-identical:\ngot  %q\nwant %q", got, want)
			}
		})
	}
	t.Run("writes too", func(t *testing.T) {
		got := h.ok(t, "source", "add", "https://comic.example/2", "--slug", "second", "-o", "json")
		var s client.Source
		if err := json.Unmarshal([]byte(got), &s); err != nil || s.Slug != "second" {
			t.Errorf("%q: %v", got, err)
		}
		if body := h.ok(t, "read", "rm", "1", "-o", "json"); body != "" {
			t.Errorf("a 204 printed %q", body)
		}
	})
}

func TestErrorsAreReadable(t *testing.T) {
	h := newHarness(t)
	h.comic(t)
	r := h.run("source", "add", "https://comic.example/1", "--slug", "Not A Slug")
	if r.err == nil {
		t.Fatal("accepted a bad slug")
	}
	if !strings.Contains(r.err.Error(), "Bad Request") || !strings.Contains(r.err.Error(), "/slug:") {
		t.Errorf("error = %q", r.err)
	}
	if !strings.Contains(r.stderr, "Error: Bad Request") {
		t.Errorf("stderr = %q", r.stderr)
	}

	if r := h.run("source", "get", "99"); r.err == nil || !strings.Contains(r.err.Error(), "source 99 does not exist") {
		t.Errorf("missing: %v", r.err)
	}
	h.ok(t, "read", "create", "comic", "--daily", "07:00")
	if r := h.run("read", "jump", "1", "0"); r.err == nil || !strings.Contains(r.err.Error(), "only moves forward") {
		t.Errorf("backward: %v", r.err)
	}
	if r := run("source", "list", "--url", h.url); r.err == nil || !strings.Contains(r.err.Error(), "Unauthorized") {
		t.Errorf("no token: %v", r.err)
	}
	if err := check(http.StatusBadGateway, []byte("<html>")); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("a non-problem body: %v", err)
	}
}

func TestBadArguments(t *testing.T) {
	h := newHarness(t)
	tests := [][]string{
		{"no-such-command"},
		{"read", "get", "abc"},
		{"read", "get", "0"},
		{"read", "skip", "1", "many"},
		{"read", "jump", "1", "last"},
		{"source", "add", "https://a.example/", "--slug", "s", "--kind", "song"},
		{"source", "probe", "https://a.example/", "--selector", "nonsense"},
		{"source", "probe", "https://a.example/", "--selector", "colour=red"},
		{"source", "list", "-o", "yaml"},
		{"token", "add", "1"},
	}
	for _, args := range tests {
		if r := h.run(args...); r.err == nil {
			t.Errorf("andante %s succeeded", strings.Join(args, " "))
		}
	}
}

func TestEnvironment(t *testing.T) {
	h := newHarness(t)
	t.Setenv("ANDANTE_URL", h.url)
	t.Setenv("ANDANTE_TOKEN", h.token)
	if r := run("source", "list"); r.err != nil {
		t.Errorf("source list: %v", r.err)
	}
}

func TestDefaultURL(t *testing.T) {
	tests := []struct {
		name, url, base, listen, want string
	}{
		{"no server beside it", "", "", "", defaultURL},
		{"server at the top of its host", "", "https://feeds.example.com", "", defaultURL},
		{"server under a path", "", "https://example.com/feeds/", "", "http://127.0.0.1:8080/feeds"},
		{"server on another port", "", "https://example.com/feeds", "0.0.0.0:9090", "http://127.0.0.1:9090/feeds"},
		{"server on every IPv6 address", "", "https://example.com", "[::]:9090", "http://127.0.0.1:9090"},
		{"server on one address", "", "https://example.com", "10.0.0.5:8080", "http://10.0.0.5:8080"},
		{"IPv6 loopback", "", "https://example.com", "[::1]:8080", "http://[::1]:8080"},
		{"listener it can't read", "", "https://example.com/feeds", "8080", defaultURL},
		{"base URL it can't read", "", "https://example.com/%zz", "", defaultURL},
		{"ANDANTE_URL wins", "http://andante.example", "https://example.com/feeds", ":9090", "http://andante.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANDANTE_URL", tt.url)
			t.Setenv("ANDANTE_BASE_URL", tt.base)
			t.Setenv("ANDANTE_LISTEN", tt.listen)
			root := newRootCmd(io.Discard, io.Discard)
			if got := root.PersistentFlags().Lookup("url").DefValue; got != tt.want {
				t.Errorf("--url default = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		r := run("completion", shell)
		if r.err != nil || !strings.Contains(r.stdout, "andante") {
			t.Errorf("completion %s: %v", shell, r.err)
		}
	}
}

func TestTheServerBeingDown(t *testing.T) {
	r := run("source", "list", "--url", "http://127.0.0.1:1")
	if r.err == nil {
		t.Error("succeeded with no server")
	}
}

func TestHelpMentionsEveryCommand(t *testing.T) {
	r := run("--help")
	for _, name := range []string{"source", "read", "token", "export", "import", "api-token", "api", "version", "completion"} {
		if !strings.Contains(r.stdout, name) {
			t.Errorf("no %s in help", name)
		}
	}
}

func TestWhoAmI(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "whoami")
	if !strings.Contains(got, "admin") {
		t.Errorf("whoami = %s", got)
	}
}

func TestDiscover(t *testing.T) {
	h := newHarness(t)
	site := sitetest.Serve(t, "discover-comic")
	got := h.ok(t, "source", "discover", site.URL+"/", "--limit", "2")
	for _, want := range []string{"The Comic", "crawl", "/1.html", "Dated:"} {
		if !strings.Contains(got, want) {
			t.Errorf("discover = %s, want %q", got, want)
		}
	}

	h.ok(t, "source", "add", site.URL+"/1.html", "--slug", "comic", "--home-url", site.URL+"/")
	if got := h.ok(t, "source", "discover", site.URL); !strings.Contains(got, "Already here:") {
		t.Errorf("the second discover = %s", got)
	}

	empty := sitetest.Serve(t, "discover-comic")
	empty.SetPage("/", "<title>Nothing</title>")
	empty.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, "<title>Nothing</title>")
			return true
		}
		return false
	})
	if got := h.ok(t, "source", "discover", empty.URL+"/"); !strings.Contains(got, "Nothing found") {
		t.Errorf("nothing to find = %s", got)
	}
	if r := h.run("source", "discover", "ftp://a.example/"); r.err == nil {
		t.Error("accepted a bad scheme")
	}
}

func TestLoginCommands(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "invite", "add", "alex")
	if !strings.Contains(got, "https://andante.example/invite/") {
		t.Errorf("invite = %s", got)
	}
	h.ok(t, "invite", "add", "pat", "--admin")
	if r := h.run("invite", "add", "admin"); r.err == nil {
		t.Error("invited a taken name")
	}
	h.ok(t, "invite", "add", "admin", "--reset")
	open := h.ok(t, "invite", "add", "--uses", "3", "--days", "2")
	if !strings.Contains(open, "Uses:") || !strings.Contains(open, "3") {
		t.Errorf("a link for three = %s", open)
	}
	if r := h.run("invite", "add", "sam", "--uses", "2"); r.err == nil {
		t.Error("made a named invite for two")
	}
	listed := h.ok(t, "invite", "list")
	if !strings.Contains(listed, "0 of 3") || !strings.Contains(listed, "admin (reset)") {
		t.Errorf("invite list = %s", listed)
	}
	h.ok(t, "invite", "rm", "1")
	if got := h.ok(t, "invite", "list"); strings.Contains(got, "alex") {
		t.Errorf("invite list after rm = %s", got)
	}

	user, err := h.store.GetUserByName(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CreateSession(t.Context(), user.ID, store.Client{IP: "203.0.113.1", UserAgent: "a browser"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := h.ok(t, "session", "list"); !strings.Contains(got, "203.0.113.1") || !strings.Contains(got, "a browser") {
		t.Errorf("sessions = %s", got)
	}
	h.ok(t, "session", "rm", "1")
	if got := h.ok(t, "session", "list"); strings.Contains(got, "203.0.113.1") {
		t.Errorf("sessions after rm = %s", got)
	}
	if got := h.ok(t, "audit"); !strings.Contains(got, "invite.created") || !strings.Contains(got, "session.revoked") {
		t.Errorf("audit = %s", got)
	}
}

func TestLogCommands(t *testing.T) {
	stack := stacktest.New(t)
	_, token := storetest.User(t, stack.Store, "admin", true)
	h := &harness{url: stack.URL, token: token, store: stack.Store}
	stacktest.Logger(stack.Logs).Info("feed request", "read", 3, "status", 200)
	stacktest.Logger(stack.CatalogueLogs).Warn("backing off", "source", "comic")

	if got := h.ok(t, "logs", "--read", "3"); !strings.Contains(got, "feed request") || !strings.Contains(got, "read=3 status=200") {
		t.Errorf("logs = %s", got)
	}
	if got := h.ok(t, "logs", "--catalogue", "--level", "warn", "--source", "comic"); !strings.Contains(got, "backing off") {
		t.Errorf("catalogue logs = %s", got)
	}

	h.ok(t, "source", "add", "https://comic.example/1", "--slug", "comic", "--title", "A Comic")
	sources, err := stack.Store.ListSourceIDs(t.Context())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %v, %v", sources, err)
	}
	failed := "connection refused"
	if err := stack.Catalogue.RecordFetch(t.Context(), catalogue.InsertCrawlLogParams{SourceID: sources[0], Url: ptr("https://comic.example/1"), Error: &failed}); err != nil {
		t.Fatal(err)
	}
	if got := h.ok(t, "source", "fetches", "comic"); !strings.Contains(got, "https://comic.example/1") || !strings.Contains(got, failed) {
		t.Errorf("fetches = %s", got)
	}
	err = stack.Catalogue.RecordEvent(t.Context(), cstore.CrawlEvent{
		SourceID: sources[0], Level: "warn", Message: "no next link on https://comic.example/1", URL: "https://comic.example/1",
		Page: []byte("<p>one\tpage</p>"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := h.ok(t, "source", "crawl-log", "comic")
	if !strings.Contains(got, "warn*") || !strings.Contains(got, "no next link on https://comic.example/1") || !strings.Contains(got, failed) {
		t.Errorf("crawl-log = %s", got)
	}
	if got := h.ok(t, "source", "crawl-log", "comic", "--kind", "fetch"); strings.Contains(got, "no next link") {
		t.Errorf("crawl-log --kind fetch = %s", got)
	}
	got = h.ok(t, "source", "crawl-log", "comic", "--has-page")
	if !strings.Contains(got, "warn*") || strings.Contains(got, failed) {
		t.Errorf("crawl-log --has-page = %s", got)
	}
	var id string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "warn*") {
			id = strings.Fields(line)[0]
		}
	}
	if got := h.ok(t, "source", "crawled-page", "comic", id); got != "<p>one\tpage</p>\n" {
		t.Errorf("crawled-page = %q", got)
	}
	if r := h.run("source", "crawled-page", "comic", "x"); r.err == nil {
		t.Error("accepted an event ID that is not a number")
	}
}

func TestSettingsCommands(t *testing.T) {
	h := newHarness(t)
	if got := h.ok(t, "settings"); !strings.Contains(got, "false") {
		t.Errorf("settings = %s", got)
	}
	if got := h.ok(t, "settings", "set", "--require-two-factor"); !strings.Contains(got, "true") {
		t.Errorf("set = %s", got)
	}
	if got := h.ok(t, "settings", "set"); !strings.Contains(got, "true") {
		t.Errorf("set with nothing changed = %s", got)
	}
	if got := h.ok(t, "settings", "set", "--require-two-factor=false"); !strings.Contains(got, "false") {
		t.Errorf("set back = %s", got)
	}
	got := h.ok(t, "settings", "set", "--who-can-invite", "users", "--invite-limit", "7")
	if !strings.Contains(got, "users") || !strings.Contains(got, "7") {
		t.Errorf("set invites = %s", got)
	}
	if r := h.run("settings", "set", "--who-can-invite", "everyone"); r.err == nil {
		t.Error("accepted who can invite = everyone")
	}
	got = h.ok(t, "settings", "set", "--password-min-length", "20", "--password-non-letters", "2", "--password-classes", "3")
	if !strings.Contains(got, "Password min length:   20") || !strings.Contains(got, "Password non-letters:  2") || !strings.Contains(got, "Password classes:      3") {
		t.Errorf("set password policy = %s", got)
	}
	if r := h.run("settings", "set", "--password-min-length", "6"); r.err == nil {
		t.Error("accepted a password minimum of 6")
	}
}

func TestUserCommands(t *testing.T) {
	h := newHarness(t)
	_, _ = storetest.User(t, h.store, "bob", false)
	if got := h.ok(t, "user", "list"); !strings.Contains(got, "bob") || !strings.Contains(got, "admin") {
		t.Errorf("user list = %s", got)
	}
	if got := h.ok(t, "user", "edit", "2", "--disable"); !strings.Contains(got, "Disabled:") || strings.Contains(got, "Disabled:  -") {
		t.Errorf("user edit --disable = %s", got)
	}
	h.ok(t, "user", "edit", "2", "--enable", "--role", "admin")
	if r := h.run("user", "edit", "2", "--disable", "--enable"); r.err == nil {
		t.Error("accepted --disable and --enable together")
	}
	if r := h.run("user", "rm", "2"); r.err == nil {
		t.Error("deleted without --yes")
	}
	h.ok(t, "user", "rm", "2", "--yes")
}

func TestBootstrap(t *testing.T) {
	token := strings.Repeat("b", 32)
	stack := stacktest.NewWith(t, stacktest.Options{BootstrapToken: token})
	bootstrap := func(stdin string, args ...string) result {
		var stdout, stderr bytes.Buffer
		root := newRootCmd(&stdout, &stderr)
		root.SetIn(strings.NewReader(stdin))
		root.SetArgs(append([]string{"bootstrap", "--url", stack.URL}, args...))
		err := root.Execute()
		return result{stdout.String(), stderr.String(), err}
	}
	if r := bootstrap(""); r.err == nil {
		t.Error("no name, and it carried on")
	}
	if r := bootstrap("", "dave"); r.err == nil || !strings.Contains(r.err.Error(), "ANDANTE_BOOTSTRAP_TOKEN") {
		t.Errorf("no token = %v", r.err)
	}
	if r := bootstrap("guess\n", "dave"); r.err == nil {
		t.Error("a wrong token worked")
	}
	r := bootstrap(token+"\n", "dave")
	if r.err != nil || !strings.Contains(r.stdout, "API token:") || !strings.Contains(r.stdout, "https://andante.example/invite/") {
		t.Fatalf("bootstrap: %v\n%s%s", r.err, r.stdout, r.stderr)
	}
	t.Setenv("ANDANTE_BOOTSTRAP_TOKEN", token)
	if r := bootstrap("", "sam"); r.err == nil {
		t.Error("a second admin was made")
	}
}

// A server under a path of its host takes --url with that path.
func TestURLWithAPath(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	_ = run("whoami", "--url", srv.URL+"/feedslow/", "--token", "x")
	if got != "/feedslow/api/v1/me" {
		t.Errorf("asked for %q", got)
	}
}

func ptr[T any](v T) *T { return &v }
