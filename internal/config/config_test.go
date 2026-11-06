// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package config

import (
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/buildinfo"
)

var required = []string{"--database-url=postgres://localhost/andante", "--base-url=https://andante.example/"}

func parse(t *testing.T, args ...string) (*Config, error) {
	t.Helper()
	var c Config
	parser, err := kong.New(&c, kong.Exit(func(int) { t.Fatal("kong tried to exit") }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Parse(args)
	return &c, err
}

func TestDefaults(t *testing.T) {
	c, err := parse(t, required...)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.AdminListen != "127.0.0.1:8081" {
		t.Errorf("listeners = %q, %q", c.Listen, c.AdminListen)
	}
	if c.DefaultTimezone != "UTC" || c.LogFormat != "json" || c.LogLevel != "info" {
		t.Errorf("timezone, format, level = %q, %q, %q", c.DefaultTimezone, c.LogFormat, c.LogLevel)
	}
	if c.CrawlInterval != 5*time.Second {
		t.Errorf("crawl interval = %s", c.CrawlInterval)
	}
}

func TestEnvVars(t *testing.T) {
	envs := map[string]string{
		"ANDANTE_DATABASE_URL":           "postgres://db/andante",
		"ANDANTE_LISTEN":                 ":1",
		"ANDANTE_ADMIN_LISTEN":           ":2",
		"ANDANTE_BASE_URL":               "https://env.example",
		"ANDANTE_DEFAULT_TIMEZONE":       "Europe/Dublin",
		"ANDANTE_USER_AGENT":             "agent",
		"ANDANTE_ADMIN_TOKEN":            "token",
		"ANDANTE_LOG_FORMAT":             "text",
		"ANDANTE_LOG_LEVEL":              "debug",
		"ANDANTE_CRAWL_INTERVAL":         "1s",
		"ANDANTE_READ_HEADER_TIMEOUT":    "2s",
		"ANDANTE_READ_TIMEOUT":           "3s",
		"ANDANTE_WRITE_TIMEOUT":          "4s",
		"ANDANTE_IDLE_TIMEOUT":           "5s",
		"ANDANTE_SHUTDOWN_TIMEOUT":       "6s",
		"ANDANTE_READINESS_TIMEOUT":      "7s",
		"ANDANTE_CRAWL_LOOKAHEAD":        "8",
		"ANDANTE_CRAWL_MAX_BODY":         "9",
		"ANDANTE_CRAWL_ALLOW_PRIVATE":    "true",
		"ANDANTE_TAIL_INTERVAL":          "10s",
		"ANDANTE_CRAWL_POLL_INTERVAL":    "11s",
		"ANDANTE_CRAWL_REQUEST_TIMEOUT":  "12s",
		"ANDANTE_CRAWL_BACKOFF_CAP":      "13s",
		"ANDANTE_ROBOTS_TTL":             "14s",
		"ANDANTE_PROBE_TIMEOUT":          "15s",
		"ANDANTE_REPLAY_MIN_DATED_SHARE": "0.5",
	}
	for k, v := range envs {
		t.Setenv(k, v)
	}
	got, err := parse(t)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		DatabaseURL: "postgres://db/andante", Listen: ":1", AdminListen: ":2",
		BaseURL: "https://env.example", DefaultTimezone: "Europe/Dublin", UserAgent: "agent",
		AdminToken: "token", LogFormat: "text", LogLevel: "debug",
		CrawlInterval: time.Second, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second,
		WriteTimeout: 4 * time.Second, IdleTimeout: 5 * time.Second, ShutdownTimeout: 6 * time.Second,
		ReadinessTimeout: 7 * time.Second, CrawlLookahead: 8, CrawlMaxBody: 9, CrawlAllowPrivate: true,
		TailInterval: 10 * time.Second, CrawlPollInterval: 11 * time.Second, CrawlRequestTimeout: 12 * time.Second,
		CrawlBackoffCap: 13 * time.Second, RobotsTTL: 14 * time.Second, ProbeTimeout: 15 * time.Second, ReplayMinDatedShare: 0.5,
	}
	if *got != want {
		t.Errorf("got %+v\nwant %+v", *got, want)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("ANDANTE_LISTEN", ":1")
	c, err := parse(t, append(required, "--listen=:2")...)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":2" {
		t.Errorf("listen = %q, want :2", c.Listen)
	}
}

func TestRejected(t *testing.T) {
	const db, base = "--database-url=postgres://localhost/andante", "--base-url=https://andante.example/"
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing database URL", []string{base}, "database-url"},
		{"missing base URL", []string{db}, "base-url"},
		{"bad DSN", []string{"--database-url=postgres://u:hunter2@host:notaport/db", base}, "database-url"},
		{"relative base URL", []string{db, "--base-url=/feeds"}, "base-url"},
		{"base URL with no host", []string{db, "--base-url=https://"}, "base-url"},
		{"non-http base URL", []string{db, "--base-url=ftp://andante.example"}, "base-url"},
		{"unparseable base URL", []string{db, "--base-url=http://[::1"}, "base-url"},
		{"unknown time zone", []string{db, base, "--default-timezone=Mars/Olympus"}, "default-timezone"},
		{"unknown log format", []string{db, base, "--log-format=xml"}, "log-format"},
		{"unknown log level", []string{db, base, "--log-level=loud"}, "log-level"},
		{"zero crawl interval", []string{db, base, "--crawl-interval=0s"}, "crawl-interval"},
		{"negative shutdown timeout", []string{db, base, "--shutdown-timeout=-1s"}, "shutdown-timeout"},
		{"zero readiness timeout", []string{db, base, "--readiness-timeout=0s"}, "readiness-timeout"},
		{"zero tail interval", []string{db, base, "--tail-interval=0s"}, "tail-interval"},
		{"zero lookahead", []string{db, base, "--crawl-lookahead=0"}, "crawl-lookahead"},
		{"zero body limit", []string{db, base, "--crawl-max-body=0"}, "crawl-max-body"},
		{"share above one", []string{db, base, "--replay-min-dated-share=1.5"}, "replay-min-dated-share"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse(t, tt.args...)
			if err == nil {
				t.Fatal("parse succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error %q leaks the password", err)
			}
		})
	}
}

func TestCrawlerUserAgent(t *testing.T) {
	c := Config{BaseURL: "https://andante.example/"}
	want := "andante/" + buildinfo.Version() + " (+https://andante.example)"
	if got := c.CrawlerUserAgent(); got != want {
		t.Errorf("CrawlerUserAgent() = %q, want %q", got, want)
	}
	c.UserAgent = "custom"
	if got := c.CrawlerUserAgent(); got != "custom" {
		t.Errorf("CrawlerUserAgent() = %q, want custom", got)
	}
}
