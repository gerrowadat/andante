// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Catalogue holds andante-catalogue's bootstrap settings, parsed by kong.
type Catalogue struct {
	DatabaseURL string `name:"database-url" env:"ANDANTE_CATALOGUE_DATABASE_URL" required:"" help:"Postgres DSN."`
	Listen      string `name:"listen" env:"ANDANTE_CATALOGUE_LISTEN" default:"127.0.0.1:8082" help:"Listener for the API, health and metrics."`
	APIToken    string `name:"api-token" env:"ANDANTE_CATALOGUE_TOKEN" required:"" help:"The token andante-server must present."`
	BaseURL     string `name:"base-url" env:"ANDANTE_BASE_URL" required:"" help:"This instance's public URL, named in the User-Agent."`
	UserAgent   string `name:"user-agent" env:"ANDANTE_USER_AGENT" help:"Crawler identity. Default: andante/<version> (+<base-url>)."`
	LogFormat   string `name:"log-format" env:"ANDANTE_LOG_FORMAT" default:"json" enum:"json,text" help:"Log format: json or text."`
	LogLevel    string `name:"log-level" env:"ANDANTE_LOG_LEVEL" default:"info" enum:"debug,info,warn,error" help:"Log level."`
	LogHistory  int    `name:"log-history" env:"ANDANTE_LOG_HISTORY" default:"1000" help:"How many log records to keep in memory for admins to read. 0 keeps none."`

	ReadHeaderTimeout time.Duration `name:"read-header-timeout" env:"ANDANTE_READ_HEADER_TIMEOUT" default:"5s" help:"Time allowed to read request headers."`
	ReadTimeout       time.Duration `name:"read-timeout" env:"ANDANTE_READ_TIMEOUT" default:"30s" help:"Time allowed to read a whole request."`
	WriteTimeout      time.Duration `name:"write-timeout" env:"ANDANTE_CATALOGUE_WRITE_TIMEOUT" default:"60s" help:"Time allowed to write a response. Must outlast a held /changes request, which is up to 30s."`
	IdleTimeout       time.Duration `name:"idle-timeout" env:"ANDANTE_IDLE_TIMEOUT" default:"2m" help:"How long an idle keep-alive connection stays open."`
	ShutdownTimeout   time.Duration `name:"shutdown-timeout" env:"ANDANTE_SHUTDOWN_TIMEOUT" default:"15s" help:"Time allowed for in-flight requests on shutdown."`
	ReadinessTimeout  time.Duration `name:"readiness-timeout" env:"ANDANTE_READINESS_TIMEOUT" default:"2s" help:"Time allowed for the /readyz check."`

	CrawlInterval       time.Duration `name:"crawl-interval" env:"ANDANTE_CRAWL_INTERVAL" default:"5s" help:"Minimum gap between requests to one host."`
	CrawlLookahead      int           `name:"crawl-lookahead" env:"ANDANTE_CRAWL_LOOKAHEAD" default:"50" help:"How far ahead of the furthest read to keep a source indexed."`
	CrawlMaxBody        int64         `name:"crawl-max-body" env:"ANDANTE_CRAWL_MAX_BODY" default:"5242880" help:"Largest response body to accept, in bytes."`
	CrawlAllowPrivate   bool          `name:"crawl-allow-private" env:"ANDANTE_CRAWL_ALLOW_PRIVATE" help:"Allow crawling private and loopback addresses. For tests and trusted networks."`
	TailInterval        time.Duration `name:"tail-interval" env:"ANDANTE_TAIL_INTERVAL" default:"6h" help:"How often to check a source at its live end."`
	CrawlPollInterval   time.Duration `name:"crawl-poll-interval" env:"ANDANTE_CRAWL_POLL_INTERVAL" default:"30s" help:"How often the crawl loop looks for work when idle."`
	CrawlRequestTimeout time.Duration `name:"crawl-request-timeout" env:"ANDANTE_CRAWL_REQUEST_TIMEOUT" default:"30s" help:"Time allowed for one request to a site."`
	CrawlBackoffCap     time.Duration `name:"crawl-backoff-cap" env:"ANDANTE_CRAWL_BACKOFF_CAP" default:"6h" help:"Longest wait after a host fails or asks for one."`
	RobotsTTL           time.Duration `name:"robots-ttl" env:"ANDANTE_ROBOTS_TTL" default:"24h" help:"How long to cache a host's robots.txt."`
	ProbeTimeout        time.Duration `name:"probe-timeout" env:"ANDANTE_PROBE_TIMEOUT" default:"5m" help:"Time allowed for a probe, which waits out --crawl-interval between pages."`
}

// longestChangeWait is the most a /changes request can be held for.
const longestChangeWait = 30 * time.Second

// Validate checks what the flags' types cannot. kong calls it after parsing.
func (c *Catalogue) Validate() error {
	var errs []error
	if c.LogHistory < 0 {
		errs = append(errs, errors.New("--log-history must not be negative"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("--database-url is required"))
	} else if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		// Not %w: the parse error can quote the DSN, password included.
		errs = append(errs, errors.New("--database-url is not a valid Postgres DSN"))
	}
	if c.APIToken == "" {
		errs = append(errs, errors.New("--api-token is required"))
	}
	if c.BaseURL == "" {
		errs = append(errs, errors.New("--base-url is required"))
	} else if err := validateURL("--base-url", c.BaseURL); err != nil {
		errs = append(errs, err)
	}
	for _, d := range []struct {
		flag  string
		value time.Duration
	}{
		{"--crawl-interval", c.CrawlInterval},
		{"--read-header-timeout", c.ReadHeaderTimeout},
		{"--read-timeout", c.ReadTimeout},
		{"--idle-timeout", c.IdleTimeout},
		{"--shutdown-timeout", c.ShutdownTimeout},
		{"--readiness-timeout", c.ReadinessTimeout},
		{"--tail-interval", c.TailInterval},
		{"--crawl-poll-interval", c.CrawlPollInterval},
		{"--crawl-request-timeout", c.CrawlRequestTimeout},
		{"--crawl-backoff-cap", c.CrawlBackoffCap},
		{"--robots-ttl", c.RobotsTTL},
		{"--probe-timeout", c.ProbeTimeout},
	} {
		if d.value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", d.flag, d.value))
		}
	}
	if c.WriteTimeout <= longestChangeWait {
		errs = append(errs, fmt.Errorf("--write-timeout must be above %s, got %s", longestChangeWait, c.WriteTimeout))
	}
	if c.CrawlLookahead <= 0 {
		errs = append(errs, fmt.Errorf("--crawl-lookahead must be positive, got %d", c.CrawlLookahead))
	}
	if c.CrawlMaxBody <= 0 {
		errs = append(errs, fmt.Errorf("--crawl-max-body must be positive, got %d", c.CrawlMaxBody))
	}
	return errors.Join(errs...)
}

// CrawlerUserAgent returns the User-Agent the crawler sends.
func (c *Catalogue) CrawlerUserAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return fmt.Sprintf("andante/%s (+%s)", buildinfo.Version(), strings.TrimRight(c.BaseURL, "/"))
}
