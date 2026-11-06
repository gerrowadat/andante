// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/temoto/robotstxt"
)

const (
	maxRedirects   = 5
	robotsMaxBytes = 500 * 1024 // RFC 9309 requires parsing at least this much.
	robotsAgent    = "andante"
)

// Errors a fetch can end in.
var (
	ErrDisallowed   = errors.New("disallowed by robots.txt")
	ErrAddress      = errors.New("refused to connect to a private address")
	ErrScheme       = errors.New("only http and https URLs can be fetched")
	ErrTooManyHops  = errors.New("too many redirects")
	ErrBodyTooLarge = errors.New("response body is too large")
)

// BackoffError means a host asked andante to wait, or is failing, and nothing
// will be requested from it until Until.
type BackoffError struct {
	Host  string
	Until time.Time
	Cause string
}

func (e *BackoffError) Error() string {
	return fmt.Sprintf("backing off %s until %s: %s", e.Host, e.Until.Format(time.RFC3339), e.Cause)
}

// StatusError is a response that is neither a page nor a reason to back off.
type StatusError struct {
	URL    string
	Status int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s returned %d", e.URL, e.Status) }

// Validators make a request conditional.
type Validators struct {
	ETag         string
	LastModified string
}

// Response is a fetched page.
type Response struct {
	URL          string // After any redirects.
	Status       int
	Body         []byte
	ETag         string
	LastModified string
	NotModified  bool
}

// FetcherOptions configure a Fetcher.
type FetcherOptions struct {
	UserAgent    string
	Interval     time.Duration // Minimum gap between requests to one host.
	Timeout      time.Duration // For one request.
	BackoffCap   time.Duration
	RobotsTTL    time.Duration
	MaxBody      int64
	AllowPrivate bool // Allow private and loopback addresses. Tests need this.
	Clock        Clock
	// AddressAllowed overrides which addresses may be dialled.
	AddressAllowed func(netip.Addr) bool
}

// Fetcher is the one way andante requests other people's pages. Every strategy
// shares one, so politeness holds per host across all of them.
type Fetcher struct {
	opts   FetcherOptions
	client *http.Client

	mu    sync.Mutex
	hosts map[string]*hostState
}

type hostState struct {
	// slot allows one request in flight.
	slot chan struct{}
	// The rest are guarded by holding slot.
	lastRequest  time.Time
	blockedUntil time.Time
	failures     int
	robots       *robotstxt.RobotsData
	robotsExpiry time.Time
}

// NewFetcher returns a Fetcher.
func NewFetcher(opts FetcherOptions) *Fetcher {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	allowed := opts.AddressAllowed
	if allowed == nil {
		allowed = PublicAddress
		if opts.AllowPrivate {
			allowed = func(netip.Addr) bool { return true }
		}
	}
	dialer := &net.Dialer{Timeout: opts.Timeout, Control: func(_, address string, _ syscall.RawConn) error {
		// This runs after DNS and before connecting, for every address tried, so
		// neither a hostname nor a redirect can get around it.
		addrPort, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrAddress, address)
		}
		if !allowed(addrPort.Addr().Unmap()) {
			return fmt.Errorf("%w: %s", ErrAddress, addrPort.Addr())
		}
		return nil
	}}
	return &Fetcher{
		opts:  opts,
		hosts: make(map[string]*hostState),
		client: &http.Client{
			Timeout: opts.Timeout,
			// Fetch follows redirects itself, so that each hop goes through
			// robots.txt and the host's politeness.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				DialContext:         dialer.DialContext,
				TLSHandshakeTimeout: opts.Timeout,
				MaxIdleConnsPerHost: 1,
			},
		},
	}
}

// PublicAddress reports whether an address is on the public internet. A source
// URL is user input, so it must not reach the machine or network andante runs on.
func PublicAddress(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, block := range nonPublic {
		if block.Contains(a) {
			return false
		}
	}
	return true
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "This network".
	netip.MustParsePrefix("100.64.0.0/10"), // Carrier-grade NAT, and Tailscale.
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments.
	netip.MustParsePrefix("198.18.0.0/15"), // Benchmarking.
	netip.MustParsePrefix("240.0.0.0/4"),   // Reserved.
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64, which can embed a private IPv4 address.
}

// Fetch requests a page, following redirects. Validators apply to the first hop.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, validators Validators) (*Response, error) {
	current := rawURL
	for range maxRedirects + 1 {
		resp, redirect, err := f.hop(ctx, current, validators)
		if err != nil {
			return nil, err
		}
		if redirect == "" {
			return resp, nil
		}
		current, validators = redirect, Validators{}
	}
	return nil, fmt.Errorf("%w from %s", ErrTooManyHops, rawURL)
}

func (f *Fetcher) host(key string) *hostState {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hosts[key]
	if !ok {
		h = &hostState{slot: make(chan struct{}, 1)}
		f.hosts[key] = h
	}
	return h
}

// hop makes one request, holding the host's slot, and returns the page or where
// it redirects to.
func (f *Fetcher) hop(ctx context.Context, rawURL string, validators Validators) (*Response, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", fmt.Errorf("%w: %s", ErrScheme, rawURL)
	}

	h := f.host(u.Scheme + "://" + u.Host)
	select {
	case h.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-h.slot }()

	if err := f.checkBackoff(h, u.Host); err != nil {
		return nil, "", err
	}
	if err := f.checkRobots(ctx, h, u); err != nil {
		return nil, "", err
	}
	if err := f.space(ctx, h); err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)
	if validators.ETag != "" {
		req.Header.Set("If-None-Match", validators.ETag)
	}
	if validators.LastModified != "" {
		req.Header.Set("If-Modified-Since", validators.LastModified)
	}

	h.lastRequest = f.opts.Clock.Now()
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrAddress) {
			return nil, "", fmt.Errorf("%s: %w", u.Hostname(), ErrAddress)
		}
		return nil, "", fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return f.handle(h, u, resp)
}

func (f *Fetcher) checkBackoff(h *hostState, host string) error {
	if until := h.blockedUntil; f.opts.Clock.Now().Before(until) {
		return &BackoffError{Host: host, Until: until, Cause: "waiting out an earlier response"}
	}
	return nil
}

// space waits out the gap since the host's last request.
func (f *Fetcher) space(ctx context.Context, h *hostState) error {
	if h.lastRequest.IsZero() {
		return nil
	}
	wait := h.lastRequest.Add(f.opts.Interval).Sub(f.opts.Clock.Now())
	if wait <= 0 {
		return nil
	}
	return f.opts.Clock.Sleep(ctx, wait)
}

func (f *Fetcher) handle(h *hostState, u *url.URL, resp *http.Response) (*Response, string, error) {
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return nil, "", f.backOff(h, u.Host, resp.Status, retryAfter(resp.Header.Get("Retry-After"), f.opts.Clock.Now()))
	case resp.StatusCode >= http.StatusInternalServerError:
		return nil, "", f.backOff(h, u.Host, resp.Status, 0)
	case resp.StatusCode == http.StatusNotModified:
		h.failures = 0
		return &Response{URL: u.String(), Status: resp.StatusCode, NotModified: true}, "", nil
	case resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest:
		location, err := resp.Location()
		if err != nil {
			return nil, "", fmt.Errorf("redirect from %s: %w", u, err)
		}
		return nil, location.String(), nil
	case resp.StatusCode >= http.StatusBadRequest:
		return nil, "", &StatusError{URL: u.String(), Status: resp.StatusCode}
	}

	h.failures = 0
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.opts.MaxBody+1))
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", u, err)
	}
	if int64(len(body)) > f.opts.MaxBody {
		return nil, "", fmt.Errorf("%w: %s", ErrBodyTooLarge, u)
	}
	return &Response{
		URL:          u.String(),
		Status:       resp.StatusCode,
		Body:         body,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, "", nil
}

// backOff blocks the host. With no delay from the host itself the wait doubles
// with each failure, up to the cap.
func (f *Fetcher) backOff(h *hostState, host, cause string, delay time.Duration) error {
	h.failures++
	if delay <= 0 {
		delay = f.opts.BackoffCap
		// The cap is reached long before the shift could overflow.
		if h.failures < 32 {
			if doubled := f.opts.Interval << h.failures; doubled > 0 && doubled < delay {
				delay = doubled
			}
		}
	}
	delay = min(delay, f.opts.BackoffCap)
	h.blockedUntil = f.opts.Clock.Now().Add(delay)
	return &BackoffError{Host: host, Until: h.blockedUntil, Cause: cause}
}

// retryAfter parses a Retry-After header, in seconds or as a date. A header it
// cannot read gives 0, which falls back to exponential backoff.
func retryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return max(time.Duration(seconds)*time.Second, 0)
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(when.Sub(now), 0)
	}
	return 0
}

// checkRobots applies the host's robots.txt, fetching it at most once per TTL.
func (f *Fetcher) checkRobots(ctx context.Context, h *hostState, u *url.URL) error {
	if h.robots == nil || !f.opts.Clock.Now().Before(h.robotsExpiry) {
		if err := f.fetchRobots(ctx, h, u); err != nil {
			return err
		}
	}
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	if !h.robots.TestAgent(target, robotsAgent) {
		return fmt.Errorf("%w: %s", ErrDisallowed, u)
	}
	return nil
}

func (f *Fetcher) fetchRobots(ctx context.Context, h *hostState, u *url.URL) error {
	robotsURL := url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), nil)
	if err != nil {
		return fmt.Errorf("build robots.txt request: %w", err)
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)

	if err := f.space(ctx, h); err != nil {
		return err
	}
	h.lastRequest = f.opts.Clock.Now()

	// Unlike a page, robots.txt follows its redirects here: RFC 9309 expects at
	// least five, and the dialler still refuses private addresses.
	client := *f.client
	client.CheckRedirect = nil
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrAddress) {
			return fmt.Errorf("%s: %w", u.Hostname(), ErrAddress)
		}
		return fmt.Errorf("fetch robots.txt of %s: %w", u.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// RFC 9309: a server error means assume everything is disallowed for now,
	// and that is what backing off does, without fixing the answer for a day.
	if resp.StatusCode >= http.StatusInternalServerError {
		return f.backOff(h, u.Host, "robots.txt returned "+resp.Status, retryAfter(resp.Header.Get("Retry-After"), f.opts.Clock.Now()))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, robotsMaxBytes))
	if err != nil {
		return fmt.Errorf("read robots.txt of %s: %w", u.Host, err)
	}
	// Also RFC 9309: any other unavailable robots.txt means no restrictions.
	robots, err := robotstxt.FromStatusAndBytes(resp.StatusCode, body)
	if err != nil {
		robots, _ = robotstxt.FromStatusAndBytes(http.StatusNotFound, nil)
	}
	h.robots = robots
	h.robotsExpiry = f.opts.Clock.Now().Add(f.opts.RobotsTTL)
	return nil
}
