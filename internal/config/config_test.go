// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/buildinfo"
)

var secret = strings.Repeat("s", 32)

var required = []string{"--database-url=postgres://localhost/andante", "--base-url=https://andante.example/", "--catalogue-url=http://catalogue.example:8082/v1", "--catalogue-token=token", "--secret=" + secret}

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
	if c.Listen != ":8080" || c.OpsListen != "127.0.0.1:8081" {
		t.Errorf("listeners = %q, %q", c.Listen, c.OpsListen)
	}
	if c.DefaultTimezone != "UTC" || c.LogFormat != "json" || c.LogLevel != "info" {
		t.Errorf("timezone, format, level = %q, %q, %q", c.DefaultTimezone, c.LogFormat, c.LogLevel)
	}
	if !c.PasswordLogin || len(c.TrustedProxies) != 0 {
		t.Errorf("password login = %v, trusted proxies = %v", c.PasswordLogin, c.TrustedProxies)
	}
	if c.DemandInterval != 30*time.Second {
		t.Errorf("demand interval = %s", c.DemandInterval)
	}
}

func TestEnvVars(t *testing.T) {
	envs := map[string]string{
		"ANDANTE_DATABASE_URL":           "postgres://db/andante",
		"ANDANTE_LISTEN":                 ":1",
		"ANDANTE_OPS_LISTEN":             ":2",
		"ANDANTE_BASE_URL":               "https://env.example",
		"ANDANTE_DEFAULT_TIMEZONE":       "Europe/Dublin",
		"ANDANTE_SECRET":                 secret,
		"ANDANTE_LOG_FORMAT":             "text",
		"ANDANTE_LOG_LEVEL":              "debug",
		"ANDANTE_READ_HEADER_TIMEOUT":    "2s",
		"ANDANTE_READ_TIMEOUT":           "3s",
		"ANDANTE_WRITE_TIMEOUT":          "4s",
		"ANDANTE_IDLE_TIMEOUT":           "5s",
		"ANDANTE_SHUTDOWN_TIMEOUT":       "6s",
		"ANDANTE_READINESS_TIMEOUT":      "7s",
		"ANDANTE_CATALOGUE_URL":          "http://catalogue.env:8082/v1",
		"ANDANTE_CATALOGUE_TOKEN":        "catalogue-token",
		"ANDANTE_DEMAND_INTERVAL":        "17s",
		"ANDANTE_PROBE_TIMEOUT":          "15s",
		"ANDANTE_REPLAY_MIN_DATED_SHARE": "0.5",
		"ANDANTE_FEED_RATE_LIMIT":        "16",
		"ANDANTE_OIDC_ISSUER":            "https://idp.example",
		"ANDANTE_OIDC_CLIENT_ID":         "andante",
		"ANDANTE_OIDC_CLIENT_SECRET":     "shh",
		"ANDANTE_OIDC_ALLOWED_DOMAIN":    "example.com,example.org",
		"ANDANTE_OIDC_ALLOWED_GROUP":     "readers",
		"ANDANTE_BOOTSTRAP_TOKEN":        secret,
	}
	for k, v := range envs {
		t.Setenv(k, v)
	}
	got, err := parse(t)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		DatabaseURL: "postgres://db/andante", Listen: ":1", OpsListen: ":2",
		BaseURL: "https://env.example", DefaultTimezone: "Europe/Dublin",
		Secret: secret, PasswordLogin: true, LogFormat: "text", LogLevel: "debug",
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second,
		WriteTimeout: 4 * time.Second, IdleTimeout: 5 * time.Second, ShutdownTimeout: 6 * time.Second,
		ReadinessTimeout: 7 * time.Second, ProbeTimeout: 15 * time.Second,
		CatalogueURL: "http://catalogue.env:8082/v1", CatalogueToken: "catalogue-token", DemandInterval: 17 * time.Second, ReplayMinDatedShare: 0.5, FeedRateLimit: 16,
		OIDCIssuer: "https://idp.example", OIDCClientID: "andante", OIDCClientSecret: "shh",
		OIDCAllowedDomain: []string{"example.com", "example.org"}, OIDCAllowedGroup: []string{"readers"},
		BootstrapToken: secret,
	}
	if !reflect.DeepEqual(*got, want) {
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
	const cat, tok = "--catalogue-url=http://c.example/v1", "--catalogue-token=token"
	key := "--secret=" + secret
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing database URL", []string{base, cat, tok, key}, "database-url"},
		{"missing base URL", []string{db, cat, tok, key}, "base-url"},
		{"missing catalogue URL", []string{db, base, tok, key}, "catalogue-url"},
		{"missing catalogue token", []string{db, base, cat, key}, "catalogue-token"},
		{"non-http catalogue URL", []string{db, base, tok, key, "--catalogue-url=ftp://c.example"}, "catalogue-url"},
		{"missing secret", []string{db, base, cat, tok}, "secret"},
		{"bad trusted proxy", []string{db, base, cat, tok, key, "--trusted-proxies=nonsense"}, "trusted-proxies"},
		{"short secret", []string{db, base, cat, tok, "--secret=short"}, "secret"},
		{"short bootstrap token", []string{db, base, cat, tok, key, "--bootstrap-token=short"}, "bootstrap-token"},
		{"bad DSN", []string{"--database-url=postgres://u:hunter2@host:notaport/db", base, cat, tok, key}, "database-url"},
		{"negative test clock", []string{db, base, cat, tok, key, "--test-clock-offset=-1h"}, "test-clock-offset"},
		{"relative base URL", []string{db, cat, tok, key, "--base-url=/feeds"}, "base-url"},
		{"base URL with no host", []string{db, cat, tok, key, "--base-url=https://"}, "base-url"},
		{"non-http base URL", []string{db, cat, tok, key, "--base-url=ftp://andante.example"}, "base-url"},
		{"base URL with a query", []string{db, cat, tok, key, "--base-url=https://andante.example/feeds?x=1"}, "base-url"},
		{"base URL with an odd path", []string{db, cat, tok, key, "--base-url=https://andante.example/a%20b"}, "base-url"},
		{"unparseable base URL", []string{db, cat, tok, key, "--base-url=http://[::1"}, "base-url"},
		{"unknown time zone", []string{db, base, cat, tok, key, "--default-timezone=Mars/Olympus"}, "default-timezone"},
		{"unknown log format", []string{db, base, cat, tok, key, "--log-format=xml"}, "log-format"},
		{"unknown log level", []string{db, base, cat, tok, key, "--log-level=loud"}, "log-level"},
		{"negative shutdown timeout", []string{db, base, cat, tok, key, "--shutdown-timeout=-1s"}, "shutdown-timeout"},
		{"zero readiness timeout", []string{db, base, cat, tok, key, "--readiness-timeout=0s"}, "readiness-timeout"},
		{"share above one", []string{db, base, cat, tok, key, "--replay-min-dated-share=1.5"}, "replay-min-dated-share"},
		{"zero rate limit", []string{db, base, cat, tok, key, "--feed-rate-limit=0"}, "feed-rate-limit"},
		{"OIDC with no issuer", []string{db, base, cat, tok, key, "--oidc-client-id=a"}, "oidc-issuer"},
		{"allowed domain with no issuer", []string{db, base, cat, tok, key, "--oidc-allowed-domain=example.com"}, "oidc-issuer"},
		{"plain HTTP issuer", []string{db, base, cat, tok, key, "--oidc-issuer=http://idp.example", "--oidc-client-id=a"}, "oidc-issuer"},
		{"issuer with no client", []string{db, base, cat, tok, key, "--oidc-issuer=https://idp.example"}, "oidc-client-id"},
		{"an address for a domain", []string{db, base, cat, tok, key, "--oidc-issuer=https://idp.example", "--oidc-client-id=a", "--oidc-allowed-domain=me@example.com"}, "oidc-allowed-domain"},
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

func TestCatalogueRejected(t *testing.T) {
	const (
		db   = "--database-url=postgres://localhost/andante"
		base = "--base-url=https://andante.example/"
		svc  = "--api-token=abc"
	)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing database URL", []string{base, svc}, "database-url"},
		{"missing token", []string{db, base}, "api-token"},
		{"missing base URL", []string{db, svc}, "base-url"},
		{"bad DSN", []string{"--database-url=postgres://u:hunter2@host:notaport/db", base, svc}, "database-url"},
		{"write timeout shorter than a held request", []string{db, base, svc, "--write-timeout=30s"}, "write-timeout"},
		{"non-positive lookahead", []string{db, base, svc, "--crawl-lookahead=0"}, "crawl-lookahead"},
		{"non-positive body limit", []string{db, base, svc, "--crawl-max-body=0"}, "crawl-max-body"},
		{"non-positive interval", []string{db, base, svc, "--crawl-interval=0s"}, "crawl-interval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Catalogue
			parser, err := kong.New(&c, kong.Vars{"version": "test"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = parser.Parse(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse = %v, want an error about %s", err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Error("the error quoted the password")
			}
		})
	}
}

func TestCrawlerUserAgent(t *testing.T) {
	c := Catalogue{BaseURL: "https://andante.example/"}
	want := "andante/" + buildinfo.Version() + " (+https://andante.example)"
	if got := c.CrawlerUserAgent(); got != want {
		t.Errorf("CrawlerUserAgent() = %q, want %q", got, want)
	}
	c.UserAgent = "custom"
	if got := c.CrawlerUserAgent(); got != "custom" {
		t.Errorf("CrawlerUserAgent() = %q, want custom", got)
	}
}

func TestLoginFlags(t *testing.T) {
	c, err := parse(t, append(required, "--no-password-login", "--trusted-proxies=10.0.0.0/8,fd00::/8")...)
	if err != nil {
		t.Fatal(err)
	}
	if c.PasswordLogin || len(c.TrustedProxies) != 2 {
		t.Errorf("password login = %v, trusted proxies = %v", c.PasswordLogin, c.TrustedProxies)
	}
}
