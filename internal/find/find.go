// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package find turns a site's address into sources that would read it. It only ever
// suggests: nothing is saved, and a person picks. It makes its requests through the
// same fetcher as a probe, so robots.txt, spacing and the SSRF guard all apply, which
// also means it takes as long as they say.
package find

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/gerrowadat/andante/internal/canon"
	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/extract"
)

const (
	strategyFeed  = "feed"
	strategyPaged = "feed-paged"
	strategyCrawl = "crawl"

	kindPage    = "page"
	kindEpisode = "episode"
	kindArticle = "article"

	maxFeeds = 2

	// A paged feed's probe only needs enough to show what it holds. Walking the whole
	// of one here would take as long as indexing it.
	probePages = 2

	// Results of an attempt, for the metric.
	resultExisting = "existing"
	resultFound    = "found"
	resultNone     = "none"
	resultFailed   = "failed"
)

// commonFeeds is where a feed usually is, if the page doesn't link one. Each costs a
// request, so the list is short.
var commonFeeds = []string{"/feed", "/feed.xml", "/rss.xml", "/atom.xml", "/index.xml"}

// Candidate is a way of reading a site.
type Candidate struct {
	Title      string
	Kind       string
	HomeURL    string
	Strategy   string
	Config     json.RawMessage
	Sample     []discovery.Entry
	DatedShare float64
	Warnings   []string
	// ExistingSourceID is the source that is certainly this serial.
	ExistingSourceID *int64
	// SimilarSourceIDs are sources that might be, for a person to look at.
	SimilarSourceIDs []int64
}

// The steps of a look at a site, as Stage.Step.
const (
	StepFetch = "fetch" // Fetching the address itself.
	StepFeed  = "feed"  // Reading a feed the address is or links to.
	StepFeeds = "feeds" // Looking for a feed where one usually is.
	StepPaged = "paged" // Checking whether a feed has older pages.
	StepProbe = "probe" // Trying a candidate.
)

// Stage is a step a look at a site has started on. Each step is a request to the
// site at least, and they're spaced by the crawl interval, so a person waiting
// wants to see them.
type Stage struct {
	Step, Message string
}

// Report is told of each stage as it starts. It's called on Find's goroutine.
type Report func(Stage)

// Finder works out how to read a site.
type Finder struct {
	store   *store.Store
	prober  *discovery.Prober
	metrics *discovery.Metrics
}

// New returns a Finder.
func New(st *store.Store, prober *discovery.Prober, metrics *discovery.Metrics) *Finder {
	return &Finder{store: st, prober: prober, metrics: metrics}
}

func existing(s db.Source) Candidate {
	return Candidate{
		Title: s.Title, Kind: s.DefaultKind, HomeURL: s.HomeUrl, Strategy: s.Strategy, Config: s.Config,
		ExistingSourceID: &s.ID,
	}
}

// known returns what the catalogue already has at an address, without asking the site.
func (f *Finder) known(ctx context.Context, address string) (*Candidate, error) {
	source, err := f.store.GetSourceByHome(ctx, address)
	if err == nil {
		c := existing(source)
		return &c, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	for _, strategy := range []string{strategyFeed, strategyPaged, strategyCrawl} {
		source, err := f.store.GetSourceByKey(ctx, canon.KeyOf(strategy, address))
		if err == nil {
			c := existing(source)
			return &c, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return nil, nil //nolint:nilnil // Not knowing it is a normal answer.
}

// stage reports a step, if anyone's listening, and counts it either way.
func (f *Finder) stage(report Report, step, message string) {
	f.metrics.CountStage(step)
	if report != nil {
		report(Stage{Step: step, Message: message})
	}
}

// idea is a candidate that hasn't been tried yet.
type idea struct {
	title, home, strategy, kind string
	config                      map[string]any
	probeConfig                 map[string]any
	partial                     bool // It won't get the whole archive.
}

// Find returns candidates for an address, best first. None means nothing worked, and a
// person will need to find the first page. An error means the site couldn't be read.
// report, which can be nil, hears of each stage as it starts.
func (f *Finder) Find(ctx context.Context, address string, limit int, report Report) ([]Candidate, error) {
	c, err := f.known(ctx, address)
	if err != nil {
		return nil, err
	}
	if c != nil {
		f.metrics.CountFind(resultExisting)
		return []Candidate{*c}, nil
	}

	ideas, err := f.look(ctx, address, report)
	if err != nil {
		f.metrics.CountFind(resultFailed)
		return nil, err
	}
	candidates, err := f.try(ctx, ideas, limit, report)
	if err != nil {
		f.metrics.CountFind(resultFailed)
		return nil, err
	}
	if len(candidates) == 0 {
		f.metrics.CountFind(resultNone)
	} else {
		f.metrics.CountFind(resultFound)
	}
	return candidates, nil
}

// look fetches what it takes to come up with ideas.
func (f *Finder) look(ctx context.Context, address string, report Report) ([]idea, error) {
	fetcher := f.prober.Fetcher()
	f.stage(report, StepFetch, "Fetching "+address)
	resp, err := fetcher.Fetch(ctx, address)
	if err != nil {
		return nil, err
	}
	if info, ok := strategies.ReadFeed(resp.Body, resp.URL); ok {
		f.stage(report, StepFeed, "That's a feed: "+feedName(info))
		return []idea{f.feedIdea(ctx, fetcher, resp.URL, resp.URL, info, report)}, nil
	}

	site, err := extract.ReadSite(extract.Page{URL: resp.URL, Body: resp.Body})
	if err != nil {
		return nil, err
	}
	ideas := f.feedIdeas(ctx, fetcher, resp.URL, site, report)
	complete := slices.ContainsFunc(ideas, func(i idea) bool { return !i.partial })
	if site.First != "" && canon.URL(site.First) != canon.URL(resp.URL) && !complete {
		// A crawl gets everything, and a feed that's cut off doesn't, so it goes first.
		ideas = append([]idea{{
			title: site.Title, home: resp.URL, strategy: strategyCrawl, kind: kindPage,
			config: map[string]any{"strategy": strategyCrawl, "start_url": site.First},
		}}, ideas...)
	}
	return ideas, nil
}

// feedName is what a stage calls a feed.
func feedName(info strategies.FeedInfo) string {
	if info.Title != "" {
		return info.Title
	}
	return "untitled"
}

func (f *Finder) feedIdeas(ctx context.Context, fetcher discovery.PageFetcher, home string, site extract.Site, report Report) []idea {
	addresses := site.Feeds
	step, verb := StepFeed, "Reading the feed at "
	if len(addresses) == 0 {
		step, verb = StepFeeds, "No feed is linked, so trying "
		base, err := url.Parse(home)
		if err != nil {
			return nil
		}
		for _, path := range commonFeeds {
			u := *base
			u.Path, u.RawQuery, u.Fragment = path, "", ""
			addresses = append(addresses, u.String())
		}
	}

	var ideas []idea
	tried := map[string]bool{}
	for _, address := range addresses {
		if len(ideas) == maxFeeds || ctx.Err() != nil {
			break
		}
		key := canon.URL(address)
		if tried[key] {
			continue
		}
		tried[key] = true
		f.stage(report, step, verb+address)
		resp, err := fetcher.Fetch(ctx, address)
		var backoff *discovery.BackoffError
		if errors.As(err, &backoff) {
			break // A site that's refusing won't start agreeing on the next path.
		}
		if err != nil {
			continue
		}
		info, ok := strategies.ReadFeed(resp.Body, resp.URL)
		if !ok {
			continue
		}
		i := f.feedIdea(ctx, fetcher, home, resp.URL, info, report)
		if i.title == "" {
			i.title = site.Title
		}
		ideas = append(ideas, i)
		if !i.partial {
			break
		}
	}
	return ideas
}

// feedIdea works out how to read a feed. Only a feed that looks cut off needs another
// request, to see whether it's paged.
func (f *Finder) feedIdea(ctx context.Context, fetcher discovery.PageFetcher, home, address string, info strategies.FeedInfo, report Report) idea {
	if info.HomeURL != "" {
		home = info.HomeURL
	}
	i := idea{
		title: info.Title, home: home, strategy: strategyFeed, kind: kindArticle,
		config: map[string]any{"strategy": strategyFeed, "feed_url": address},
	}
	if !info.CutOff {
		return i
	}
	f.stage(report, StepPaged, "The feed only has the latest, so checking for older pages")
	if paged(ctx, fetcher, address, info) {
		i.strategy = strategyPaged
		i.config = map[string]any{"strategy": strategyPaged, "feed_url": address}
		i.probeConfig = map[string]any{"strategy": strategyPaged, "feed_url": address, "max_pages": probePages}
		return i
	}
	i.partial = true
	return i
}

func paged(ctx context.Context, fetcher discovery.PageFetcher, address string, info strategies.FeedInfo) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	query := u.Query()
	query.Set("paged", "2")
	u.RawQuery = query.Encode()
	resp, err := fetcher.Fetch(ctx, u.String())
	if err != nil {
		return false
	}
	second, ok := strategies.ReadFeed(resp.Body, resp.URL)
	return ok && second.First != info.First
}

// try probes each idea. One that fails is dropped, so a broken feed doesn't hide a
// crawl that would work. Only if they all fail is that an error.
func (f *Finder) try(ctx context.Context, ideas []idea, limit int, report Report) ([]Candidate, error) {
	var (
		candidates []Candidate
		failure    error
	)
	for n, i := range ideas {
		f.stage(report, StepProbe, fmt.Sprintf("Trying %d of %d: %s", n+1, len(ideas), i.describe()))
		c, err := f.tryOne(ctx, i, limit)
		if err != nil {
			failure = err
			continue
		}
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 && failure != nil {
		return nil, failure
	}
	return candidates, nil
}

// describe says how an idea would read the site.
func (i idea) describe() string {
	switch i.strategy {
	case strategyFeed:
		return "its feed"
	case strategyPaged:
		return "its feed, page by page"
	default:
		return "page by page, from the first"
	}
}

func (f *Finder) tryOne(ctx context.Context, i idea, limit int) (Candidate, error) {
	config, err := json.Marshal(i.config)
	if err != nil {
		return Candidate{}, fmt.Errorf("encode config: %w", err)
	}
	key, err := canon.Key(i.strategy, config)
	if err != nil {
		return Candidate{}, err
	}
	source, err := f.store.GetSourceByKey(ctx, key)
	if err == nil {
		return existing(source), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return Candidate{}, err
	}

	probing := config
	if i.probeConfig != nil {
		if probing, err = json.Marshal(i.probeConfig); err != nil {
			return Candidate{}, fmt.Errorf("encode config: %w", err)
		}
	}
	result, err := f.prober.Probe(ctx, discovery.ProbeRequest{Strategy: i.strategy, Config: probing, Kind: i.kind, Limit: limit})
	if err != nil {
		return Candidate{}, err
	}
	similar, err := f.store.SimilarSources(ctx, i.title, i.home)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		Title: i.title, Kind: kindOf(i.kind, result.Entries), HomeURL: i.home, Strategy: i.strategy, Config: config,
		Sample: result.Entries, DatedShare: result.DatedShare, Warnings: result.Warnings, SimilarSourceIDs: similar,
	}, nil
}

// kindOf prefers what the entries turned out to be, because a feed's items say whether
// they're episodes and the feed doesn't.
func kindOf(guess string, entries []discovery.Entry) string {
	if slices.ContainsFunc(entries, func(e discovery.Entry) bool { return e.Kind == kindEpisode }) {
		return kindEpisode
	}
	return guess
}
