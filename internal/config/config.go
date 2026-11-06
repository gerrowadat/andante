// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the server's bootstrap settings, parsed by kong. Each flag has an
// ANDANTE_* environment variable counterpart. See docs/configuration.md.
type Config struct {
	DatabaseURL     string `name:"database-url" env:"ANDANTE_DATABASE_URL" required:"" help:"Postgres DSN."`
	Listen          string `name:"listen" env:"ANDANTE_LISTEN" default:":8080" help:"Public listener (feeds)."`
	AdminListen     string `name:"admin-listen" env:"ANDANTE_ADMIN_LISTEN" default:"127.0.0.1:8081" help:"Admin listener (API, health, metrics)."`
	BaseURL         string `name:"base-url" env:"ANDANTE_BASE_URL" required:"" help:"Public URL used in feed links."`
	DefaultTimezone string `name:"default-timezone" env:"ANDANTE_DEFAULT_TIMEZONE" default:"UTC" help:"IANA time zone for new reads."`
	UserAgent       string `name:"user-agent" env:"ANDANTE_USER_AGENT" help:"Crawler identity. Default: andante/<version> (+<base-url>)."`
	AdminToken      string `name:"admin-token" env:"ANDANTE_ADMIN_TOKEN" help:"Bootstrap API token. Generated and logged once if unset and no API tokens exist."`
	LogFormat       string `name:"log-format" env:"ANDANTE_LOG_FORMAT" default:"json" enum:"json,text" help:"Log format: json or text."`
	LogLevel        string `name:"log-level" env:"ANDANTE_LOG_LEVEL" default:"info" enum:"debug,info,warn,error" help:"Log level."`

	CrawlInterval     time.Duration `name:"crawl-interval" env:"ANDANTE_CRAWL_INTERVAL" default:"5s" help:"Minimum gap between requests to one host."`
	ReadHeaderTimeout time.Duration `name:"read-header-timeout" env:"ANDANTE_READ_HEADER_TIMEOUT" default:"5s" help:"Time allowed to read request headers."`
	ReadTimeout       time.Duration `name:"read-timeout" env:"ANDANTE_READ_TIMEOUT" default:"30s" help:"Time allowed to read a whole request."`
	WriteTimeout      time.Duration `name:"write-timeout" env:"ANDANTE_WRITE_TIMEOUT" default:"30s" help:"Time allowed to write a response."`
	IdleTimeout       time.Duration `name:"idle-timeout" env:"ANDANTE_IDLE_TIMEOUT" default:"2m" help:"How long an idle keep-alive connection stays open."`
	ShutdownTimeout   time.Duration `name:"shutdown-timeout" env:"ANDANTE_SHUTDOWN_TIMEOUT" default:"15s" help:"Time allowed for in-flight requests on shutdown."`
	ReadinessTimeout  time.Duration `name:"readiness-timeout" env:"ANDANTE_READINESS_TIMEOUT" default:"2s" help:"Time allowed for the /readyz check."`

	CrawlLookahead      int           `name:"crawl-lookahead" env:"ANDANTE_CRAWL_LOOKAHEAD" default:"50" help:"How far ahead of the furthest read to keep a source indexed."`
	CrawlMaxBody        int64         `name:"crawl-max-body" env:"ANDANTE_CRAWL_MAX_BODY" default:"5242880" help:"Largest response body to accept, in bytes."`
	CrawlAllowPrivate   bool          `name:"crawl-allow-private" env:"ANDANTE_CRAWL_ALLOW_PRIVATE" help:"Allow crawling private and loopback addresses. For tests and trusted networks."`
	TailInterval        time.Duration `name:"tail-interval" env:"ANDANTE_TAIL_INTERVAL" default:"6h" help:"How often to check a source at its live end."`
	CrawlPollInterval   time.Duration `name:"crawl-poll-interval" env:"ANDANTE_CRAWL_POLL_INTERVAL" default:"30s" help:"How often the crawl loop looks for work when idle."`
	CrawlRequestTimeout time.Duration `name:"crawl-request-timeout" env:"ANDANTE_CRAWL_REQUEST_TIMEOUT" default:"30s" help:"Time allowed for one request to a site."`
	CrawlBackoffCap     time.Duration `name:"crawl-backoff-cap" env:"ANDANTE_CRAWL_BACKOFF_CAP" default:"6h" help:"Longest wait after a host fails or asks for one."`
	RobotsTTL           time.Duration `name:"robots-ttl" env:"ANDANTE_ROBOTS_TTL" default:"24h" help:"How long to cache a host's robots.txt."`
	ProbeTimeout        time.Duration `name:"probe-timeout" env:"ANDANTE_PROBE_TIMEOUT" default:"5m" help:"Time allowed for a probe, which waits out --crawl-interval between pages."`

	ReplayMinDatedShare float64 `name:"replay-min-dated-share" env:"ANDANTE_REPLAY_MIN_DATED_SHARE" default:"0.9" help:"Share of a source's entries that need dates before replay works without a default_gap."`
	FeedRateLimit       int     `name:"feed-rate-limit" env:"ANDANTE_FEED_RATE_LIMIT" default:"60" help:"Requests a feed token may make per hour."`

	TestClockOffset time.Duration `name:"test-clock-offset" env:"ANDANTE_TEST_CLOCK_OFFSET" help:"For the end-to-end suite only: run reads and feeds this far ahead of the real clock."`
}

// Validate checks what the flags' types cannot. kong calls it after parsing.
func (c *Config) Validate() error {
	var errs []error
	// kong runs this before its own required-flag check, so name missing flags
	// here. Otherwise an empty value would fail as if it were a malformed one.
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("--database-url is required"))
	} else if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		// Not %w: the parse error can quote the DSN, password included.
		errs = append(errs, errors.New("--database-url is not a valid Postgres DSN"))
	}
	if c.BaseURL == "" {
		errs = append(errs, errors.New("--base-url is required"))
	} else if err := validateBaseURL(c.BaseURL); err != nil {
		errs = append(errs, err)
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		errs = append(errs, fmt.Errorf("--default-timezone: %w", err))
	}
	for _, d := range []struct {
		flag  string
		value time.Duration
	}{
		{"--crawl-interval", c.CrawlInterval},
		{"--read-header-timeout", c.ReadHeaderTimeout},
		{"--read-timeout", c.ReadTimeout},
		{"--write-timeout", c.WriteTimeout},
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
	if c.CrawlLookahead <= 0 {
		errs = append(errs, fmt.Errorf("--crawl-lookahead must be positive, got %d", c.CrawlLookahead))
	}
	if c.CrawlMaxBody <= 0 {
		errs = append(errs, fmt.Errorf("--crawl-max-body must be positive, got %d", c.CrawlMaxBody))
	}
	if c.FeedRateLimit <= 0 {
		errs = append(errs, fmt.Errorf("--feed-rate-limit must be positive, got %d", c.FeedRateLimit))
	}
	if c.TestClockOffset < 0 {
		errs = append(errs, fmt.Errorf("--test-clock-offset cannot be negative, got %s", c.TestClockOffset))
	}
	if c.ReplayMinDatedShare < 0 || c.ReplayMinDatedShare > 1 {
		errs = append(errs, fmt.Errorf("--replay-min-dated-share must be from 0 to 1, got %v", c.ReplayMinDatedShare))
	}
	return errors.Join(errs...)
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("--base-url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("--base-url must be an absolute http(s) URL, got %q", raw)
	}
	return nil
}

// CrawlerUserAgent returns the User-Agent the crawler sends.
func (c *Config) CrawlerUserAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return fmt.Sprintf("andante/%s (+%s)", buildinfo.Version(), strings.TrimRight(c.BaseURL, "/"))
}
