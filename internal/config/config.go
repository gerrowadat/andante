// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/gerrowadat/andante/internal/clientip"
	"github.com/gerrowadat/andante/internal/seal"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the server's bootstrap settings, parsed by kong. Each flag has an
// ANDANTE_* environment variable counterpart. See docs/configuration.md.
type Config struct {
	DatabaseURL     string `name:"database-url" env:"ANDANTE_DATABASE_URL" required:"" help:"Postgres DSN."`
	Listen          string `name:"listen" env:"ANDANTE_LISTEN" default:":8080" help:"Public listener (feeds and the API)."`
	OpsListen       string `name:"ops-listen" env:"ANDANTE_OPS_LISTEN" default:"127.0.0.1:8081" help:"Ops listener (health, metrics)."`
	BaseURL         string `name:"base-url" env:"ANDANTE_BASE_URL" required:"" help:"Public URL used in feed links."`
	DefaultTimezone string `name:"default-timezone" env:"ANDANTE_DEFAULT_TIMEZONE" default:"UTC" help:"IANA time zone for new reads."`
	Secret          string `name:"secret" env:"ANDANTE_SECRET" required:"" help:"Key for encrypted fields, at least 32 characters. Keep it safe, and don't change it without rotate-secret."`
	LogFormat       string `name:"log-format" env:"ANDANTE_LOG_FORMAT" default:"json" enum:"json,text" help:"Log format: json or text."`
	LogLevel        string `name:"log-level" env:"ANDANTE_LOG_LEVEL" default:"info" enum:"debug,info,warn,error" help:"Log level."`

	ReadHeaderTimeout time.Duration `name:"read-header-timeout" env:"ANDANTE_READ_HEADER_TIMEOUT" default:"5s" help:"Time allowed to read request headers."`
	ReadTimeout       time.Duration `name:"read-timeout" env:"ANDANTE_READ_TIMEOUT" default:"30s" help:"Time allowed to read a whole request."`
	WriteTimeout      time.Duration `name:"write-timeout" env:"ANDANTE_WRITE_TIMEOUT" default:"30s" help:"Time allowed to write a response."`
	IdleTimeout       time.Duration `name:"idle-timeout" env:"ANDANTE_IDLE_TIMEOUT" default:"2m" help:"How long an idle keep-alive connection stays open."`
	ShutdownTimeout   time.Duration `name:"shutdown-timeout" env:"ANDANTE_SHUTDOWN_TIMEOUT" default:"15s" help:"Time allowed for in-flight requests on shutdown."`
	ReadinessTimeout  time.Duration `name:"readiness-timeout" env:"ANDANTE_READINESS_TIMEOUT" default:"2s" help:"Time allowed for the /readyz check."`

	TrustedProxies []string `name:"trusted-proxies" env:"ANDANTE_TRUSTED_PROXIES" help:"CIDRs of proxies whose X-Forwarded-For to believe."`
	PasswordLogin  bool     `name:"password-login" env:"ANDANTE_PASSWORD_LOGIN" default:"true" negatable:"" help:"Allow logging in with a password."`

	CatalogueURL   string        `name:"catalogue-url" env:"ANDANTE_CATALOGUE_URL" required:"" help:"Address of andante-catalogue."`
	CatalogueToken string        `name:"catalogue-token" env:"ANDANTE_CATALOGUE_TOKEN" required:"" help:"The catalogue's API token."`
	DemandInterval time.Duration `name:"demand-interval" env:"ANDANTE_DEMAND_INTERVAL" default:"30s" help:"How often to tell the catalogue how far readers have got."`
	ProbeTimeout   time.Duration `name:"probe-timeout" env:"ANDANTE_PROBE_TIMEOUT" default:"5m" help:"Time allowed for a probe, which waits out --crawl-interval between pages."`

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
	} else if err := validateURL("--base-url", c.BaseURL); err != nil {
		errs = append(errs, err)
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		errs = append(errs, fmt.Errorf("--default-timezone: %w", err))
	}
	for _, d := range []struct {
		flag  string
		value time.Duration
	}{
		{"--read-header-timeout", c.ReadHeaderTimeout},
		{"--read-timeout", c.ReadTimeout},
		{"--write-timeout", c.WriteTimeout},
		{"--idle-timeout", c.IdleTimeout},
		{"--shutdown-timeout", c.ShutdownTimeout},
		{"--readiness-timeout", c.ReadinessTimeout},
		{"--demand-interval", c.DemandInterval},
		{"--probe-timeout", c.ProbeTimeout},
	} {
		if d.value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", d.flag, d.value))
		}
	}
	if _, err := clientip.New(c.TrustedProxies); err != nil {
		errs = append(errs, fmt.Errorf("--trusted-proxies: %w", err))
	}
	if c.Secret == "" {
		errs = append(errs, errors.New("--secret is required"))
	} else if len(c.Secret) < seal.MinSecretLength {
		errs = append(errs, fmt.Errorf("--secret must be at least %d characters", seal.MinSecretLength))
	}
	if c.CatalogueURL == "" {
		errs = append(errs, errors.New("--catalogue-url is required"))
	} else if err := validateURL("--catalogue-url", c.CatalogueURL); err != nil {
		errs = append(errs, err)
	}
	if c.CatalogueToken == "" {
		errs = append(errs, errors.New("--catalogue-token is required"))
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

func validateURL(flag, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", flag, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an absolute http(s) URL, got %q", flag, raw)
	}
	return nil
}
