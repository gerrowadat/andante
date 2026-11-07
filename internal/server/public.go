// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gerrowadat/andante/internal/feed"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/time/rate"
)

const (
	minMaxAge = time.Minute
	maxMaxAge = time.Hour

	// csrfWindow is how long a confirmation page stays usable: this window and
	// the one before it.
	csrfWindow = 10 * time.Minute

	maxFormBytes = 4 << 10

	labelNone    = "none"
	labelUnknown = "unknown"
)

// PublicOptions configure the public listener's feed routes.
type PublicOptions struct {
	Store       *store.Store
	Reads       *reads.Service
	BaseURL     string
	RatePerHour int // Requests a token may make.
	Registry    prometheus.Registerer
	Logger      *slog.Logger
	Now         func() time.Time
}

// public serves feeds and the "next one now" control. A request here reads the
// index and never causes a crawl. The feed URL is the only credential, so an
// unknown or revoked token gets a 404, which does not confirm it ever existed.
type public struct {
	opts     PublicOptions
	requests *prometheus.CounterVec
	render   prometheus.Histogram

	mu       sync.Mutex
	limiters map[int64]*rate.Limiter
}

// NewPublic returns the handler for the public listener.
func NewPublic(opts PublicOptions) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	factory := promauto.With(opts.Registry)
	p := &public{
		opts:     opts,
		limiters: make(map[int64]*rate.Limiter),
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_feed_requests_total",
			Help: "Feed requests, by format, role and status.",
		}, []string{"format", "role", "status"}),
		render: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "andante_feed_render_duration_seconds",
			Help:    "How long rendering a feed took.",
			Buckets: prometheus.DefBuckets,
		}),
	}
	mux := http.NewServeMux()
	// ServeMux wildcards must be a whole path segment, so the three feed routes
	// are one, and the handler splits the token from the format.
	mux.HandleFunc("GET /f/{file}", p.serveFeed)
	mux.HandleFunc("GET /f/{token}/next", p.confirmNext)
	mux.HandleFunc("POST /f/{token}/next", p.releaseNext)
	return mux
}

func (p *public) count(format, role string, status int) {
	p.requests.WithLabelValues(format, role, strconv.Itoa(status)).Inc()
}

// allow applies a token's rate limit, and says how long to wait if it is used up.
func (p *public) allow(id int64, now time.Time) (bool, time.Duration) {
	p.mu.Lock()
	limiter, ok := p.limiters[id]
	if !ok {
		perHour := max(p.opts.RatePerHour, 1)
		limiter = rate.NewLimiter(rate.Limit(float64(perHour)/time.Hour.Seconds()), perHour)
		p.limiters[id] = limiter
	}
	p.mu.Unlock()

	reservation := limiter.ReserveN(now, 1)
	wait := reservation.DelayFrom(now)
	if wait > 0 {
		reservation.CancelAt(now)
		return false, wait
	}
	return true, 0
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
}

func (p *public) fail(w http.ResponseWriter, err error) {
	// Never the path or the token: both are the credential.
	p.opts.Logger.Error("serving a feed failed", "error", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (p *public) serveFeed(w http.ResponseWriter, r *http.Request) {
	raw, extension, found := cutLast(r.PathValue("file"), ".")
	format, known := feed.FormatFor(extension)
	label := labelUnknown
	if known {
		label = format.Name
	}
	if !found || !known {
		p.count(label, labelNone, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	token, err := p.opts.Store.LookupFeedToken(ctx, raw)
	if errors.Is(err, store.ErrNotFound) {
		p.count(label, labelNone, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if err != nil {
		p.fail(w, err)
		return
	}
	now := p.opts.Now()
	if ok, wait := p.allow(token.ID, now); !ok {
		p.count(label, token.Role, http.StatusTooManyRequests)
		tooMany(w, wait)
		return
	}
	if err := p.opts.Store.TouchFeedToken(ctx, token.ID); err != nil {
		p.opts.Logger.Warn("could not record a token's use", "error", err)
	}

	read, err := p.opts.Store.GetRead(ctx, store.Holder, token.ReadID)
	if err != nil {
		p.fail(w, err)
		return
	}
	view, entries, err := p.opts.Reads.Released(ctx, read, feed.Window(read))
	if err != nil {
		p.fail(w, err)
		return
	}
	segment, err := p.opts.Store.LatestSegmentID(ctx, read.ID)
	if err != nil {
		p.fail(w, err)
		return
	}

	etag := feedETag(read.ID, view.Position, segment, token.Role, format.Name)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cacheControl(view.NextRelease, now))
	if matches(r.Header.Get("If-None-Match"), etag) {
		p.count(label, token.Role, http.StatusNotModified)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	source, err := p.opts.Store.GetSource(ctx, read.SourceID)
	if err != nil {
		p.fail(w, err)
		return
	}
	start := time.Now()
	built, err := feed.Build(feed.Input{
		Read: read, Source: source, Entries: entries, Role: token.Role, Token: raw, BaseURL: p.opts.BaseURL,
	})
	if err != nil {
		p.fail(w, err)
		return
	}
	body, err := format.Render(built, feed.URL(p.opts.BaseURL, raw, format.Name))
	if err != nil {
		p.fail(w, err)
		return
	}
	p.render.Observe(time.Since(start).Seconds())
	p.count(label, token.Role, http.StatusOK)
	w.Header().Set("Content-Type", format.ContentType)
	_, _ = w.Write(body)
}

func cutLast(s, separator string) (before, after string, found bool) {
	i := strings.LastIndex(s, separator)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(separator):], true
}

func feedETag(readID int64, position int, segment int64, role, format string) string {
	hash := sha256.Sum256(fmt.Appendf(nil, "%d|%d|%d|%s|%s", readID, position, segment, role, format))
	return `"` + hex.EncodeToString(hash[:16]) + `"`
}

func matches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}

// cacheControl lets clients hold a feed until its next release, within limits.
func cacheControl(next *time.Time, now time.Time) string {
	age := maxMaxAge
	if next != nil {
		age = min(max(next.Sub(now), minMaxAge), maxMaxAge)
	}
	return "public, max-age=" + strconv.Itoa(int(age.Seconds()))
}

// csrfToken derives a form token from the feed token. A feed reader that prefetches
// the confirmation page learns nothing it did not already have, and the form
// cannot be posted from anywhere that does not know the feed URL.
func csrfToken(raw string, at time.Time) string {
	mac := hmac.New(sha256.New, []byte(raw))
	mac.Write(strconv.AppendInt([]byte("next:"), at.Unix()/int64(csrfWindow.Seconds()), 10))
	return hex.EncodeToString(mac.Sum(nil))
}

func validCSRF(raw, got string, now time.Time) bool {
	for _, at := range []time.Time{now, now.Add(-csrfWindow)} {
		if hmac.Equal([]byte(csrfToken(raw, at)), []byte(got)) {
			return true
		}
	}
	return false
}

var confirmPage = template.Must(template.New("next").Parse(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Next one now?</title></head>
<body>
  <h1>{{.Title}}</h1>
  <p>Release the next entry now?</p>
  <form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Next one now</button></form>
</body>
</html>
`))

// ownerControls looks up the token behind a control. Only an owner has controls, so
// anyone else gets the same 404 as an unknown token.
func (p *public) ownerControls(w http.ResponseWriter, r *http.Request) (string, db.FeedToken, bool) {
	raw := r.PathValue("token")
	token, err := p.opts.Store.LookupFeedToken(r.Context(), raw)
	if errors.Is(err, store.ErrNotFound) || (err == nil && token.Role != feed.RoleOwner) {
		http.NotFound(w, r)
		return "", db.FeedToken{}, false
	}
	if err != nil {
		p.fail(w, err)
		return "", db.FeedToken{}, false
	}
	if ok, wait := p.allow(token.ID, p.opts.Now()); !ok {
		tooMany(w, wait)
		return "", db.FeedToken{}, false
	}
	// This URL holds the token, and the page after it is someone else's site. That's
	// also why the policy has no form-action: browsers apply it to the redirect.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return raw, token, true
}

func (p *public) confirmNext(w http.ResponseWriter, r *http.Request) {
	raw, token, ok := p.ownerControls(w, r)
	if !ok {
		return
	}
	read, err := p.opts.Store.GetRead(r.Context(), store.Holder, token.ReadID)
	if err != nil {
		p.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := confirmPage.Execute(w, map[string]string{"Title": read.Name, "CSRF": csrfToken(raw, p.opts.Now())}); err != nil {
		p.opts.Logger.Warn("could not write the confirmation page", "error", err)
	}
}

// releaseNext is a POST, after a page that asks, because feed readers and link
// previewers fetch GET links on their own.
func (p *public) releaseNext(w http.ResponseWriter, r *http.Request) {
	raw, token, ok := p.ownerControls(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil || !validCSRF(raw, r.PostForm.Get("csrf"), p.opts.Now()) {
		http.Error(w, "this page has expired; go back and try again", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	_, err := p.opts.Reads.Adjust(ctx, store.Holder, token.ReadID, schedule.Skip{Count: 1})
	if errors.Is(err, schedule.ErrNothingToSkip) {
		http.Error(w, "there is no next entry yet", http.StatusConflict)
		return
	}
	if err != nil {
		p.fail(w, err)
		return
	}
	target, err := p.released(ctx, token.ReadID)
	if err != nil {
		p.fail(w, err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// released returns where to send someone after a release: the entry's original
// page, or the source's home page if it has none.
func (p *public) released(ctx context.Context, readID int64) (string, error) {
	read, err := p.opts.Store.GetRead(ctx, store.Holder, readID)
	if err != nil {
		return "", err
	}
	source, err := p.opts.Store.GetSource(ctx, read.SourceID)
	if err != nil {
		return "", err
	}
	_, entries, err := p.opts.Reads.Released(ctx, read, 1)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 && entries[0].Entry.Url != nil && webURL(*entries[0].Entry.Url) {
		return *entries[0].Entry.Url, nil
	}
	return source.HomeUrl, nil
}

// webURL keeps a redirect to the web. Entry URLs come from other people's pages.
func webURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}
