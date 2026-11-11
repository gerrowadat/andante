// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/pkg/client"
)

const (
	// lookTimeout is how long working out a site can take. It asks a site for one page
	// every --crawl-interval, so it's minutes.
	lookTimeout = 10 * time.Minute
	// jobLife is how long an answer is kept for someone to come back to.
	jobLife = time.Hour
	// jobsEach is how many sites one person can be looking at at once.
	jobsEach   = 3
	refreshSec = 3
	sampleShow = 5
)

// job is one look at a site. It lives in memory, because it's only useful for the
// next few minutes, and losing it on a restart only means looking again.
type job struct {
	id, address string
	owner       int64
	started     time.Time
	done        bool
	failed      string
	result      []client.Candidate
}

type jobs struct {
	mu   sync.Mutex
	byID map[string]*job
}

func newJobs() *jobs { return &jobs{byID: map[string]*job{}} }

// start makes a job, or refuses if this person has enough running already.
func (j *jobs) start(owner int64, address string, now time.Time) (*job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	running := 0
	for id, each := range j.byID {
		if now.Sub(each.started) > jobLife {
			delete(j.byID, id)
			continue
		}
		if each.owner == owner && !each.done {
			running++
		}
	}
	if running >= jobsEach {
		return nil, false
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	created := &job{id: hex.EncodeToString(b), address: address, owner: owner, started: now}
	j.byID[created.id] = created
	return created, true
}

// get returns a copy of someone's own job. Someone else's is as good as missing.
func (j *jobs) get(id string, owner int64) (job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	found, ok := j.byID[id]
	if !ok || found.owner != owner {
		return job{}, false
	}
	return *found, true
}

func (j *jobs) finish(id string, result []client.Candidate, failed string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if found, ok := j.byID[id]; ok {
		found.done, found.result, found.failed = true, result, failed
	}
}

func (s *site) showAdd(w http.ResponseWriter, _ *http.Request, v visitor) {
	s.show(w, v, http.StatusOK, addView, pageData{Title: "Add a site"})
}

func addressOf(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return raw, true
}

// startAdd looks at a site in the background, and sends the browser to a page that
// checks on it. With first set, the address is a first page to crawl from.
func (s *site) startAdd(w http.ResponseWriter, r *http.Request, v visitor) {
	address, ok := addressOf(r.PostForm.Get("url"))
	if !ok {
		s.show(w, v, http.StatusUnprocessableEntity, addView, pageData{Title: "Add a site", Error: "That isn't a web address."})
		return
	}
	created, ok := s.jobs.start(v.actor.UserID, address, s.opts.Now())
	if !ok {
		s.show(w, v, http.StatusTooManyRequests, addView, pageData{Title: "Add a site", Error: "You're looking at enough sites already. Wait for one to finish."})
		return
	}
	first := r.PostForm.Get("first") == "1"
	go s.look(created.id, address, first)
	http.Redirect(w, r, "/add/"+created.id, http.StatusSeeOther)
}

func (s *site) look(id, address string, first bool) {
	ctx, cancel := context.WithTimeout(context.Background(), lookTimeout)
	defer cancel()
	if !first {
		found, err := s.opts.App.Catalogue.Discover(ctx, client.DiscoveryRequest{URL: address})
		if err != nil {
			s.jobs.finish(id, nil, "That didn't work: "+err.Error())
			return
		}
		s.jobs.finish(id, found.Candidates, "")
		return
	}
	var config client.SourceConfig
	if err := config.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: address}); err != nil {
		s.jobs.finish(id, nil, err.Error())
		return
	}
	limit := int32(sampleShow)
	probe, err := s.opts.App.Catalogue.Probe(ctx, client.ProbeRequest{Config: config, Limit: &limit})
	if err != nil {
		s.jobs.finish(id, nil, "That didn't work: "+err.Error())
		return
	}
	title := address
	if u, err := url.Parse(address); err == nil {
		title = u.Host
	}
	s.jobs.finish(id, []client.Candidate{{
		Title: title, HomeURL: address, DefaultKind: client.Page, Config: config,
		Sample: probe.Entries, Warnings: probe.Warnings, DatedShare: probe.DatedShare,
	}}, "")
}

type candidateRow struct {
	Title, How, Kind string
	Existing         int64
	Similar          []int64
	Sample, Warnings []string
}

func how(c client.Candidate) string {
	strategy, err := c.Config.Discriminator()
	if err != nil {
		return "unknown"
	}
	return map[string]string{
		"feed": "from its feed", "feed-paged": "from its feed, page by page",
		"crawl": "page by page, by each page's next link", "numeric": "by numbered pages",
	}[strategy]
}

func (s *site) found(w http.ResponseWriter, r *http.Request, v visitor) {
	j, ok := s.jobs.get(r.PathValue("job"), v.actor.UserID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data := struct {
		ID, Address, Failed string
		Done                bool
		Candidates          []candidateRow
	}{ID: j.id, Address: j.address, Failed: j.failed, Done: j.done}
	for _, c := range j.result {
		row := candidateRow{Title: c.Title, How: how(c), Kind: string(c.DefaultKind), Similar: c.SimilarSourceIds, Warnings: c.Warnings}
		if existing, err := c.ExistingSourceID.Get(); err == nil {
			row.Existing = existing
		}
		for i, e := range c.Sample {
			if i == sampleShow {
				break
			}
			title, _ := e.Title.Get()
			if title == "" {
				title = "Entry " + strconv.Itoa(int(e.Seq))
			}
			row.Sample = append(row.Sample, title)
		}
		data.Candidates = append(data.Candidates, row)
	}
	p := pageData{Title: "Add " + j.address, Data: data}
	if !j.done {
		p.Refresh = refreshSec
	}
	s.show(w, v, http.StatusOK, foundView, p)
}

func (s *site) addCandidate(w http.ResponseWriter, r *http.Request, v visitor) {
	j, ok := s.jobs.get(r.PathValue("job"), v.actor.UserID)
	n, err := strconv.Atoi(r.PathValue("n"))
	if !ok || !j.done || err != nil || n < 0 || n >= len(j.result) {
		http.NotFound(w, r)
		return
	}
	c := j.result[n]
	if existing, err := c.ExistingSourceID.Get(); err == nil {
		http.Redirect(w, r, "/sources/"+strconv.FormatInt(existing, 10), http.StatusSeeOther)
		return
	}
	created, err := s.opts.App.CreateSourceNamed(r.Context(), replica.SourceCreate{SourceCreate: client.SourceCreate{
		Title: c.Title, HomeURL: c.HomeURL, DefaultKind: c.DefaultKind, Config: c.Config,
	}})
	var duplicate *app.ErrDuplicate
	if errors.As(err, &duplicate) {
		http.Redirect(w, r, "/sources/"+strconv.FormatInt(duplicate.SourceID, 10), http.StatusSeeOther)
		return
	}
	if err != nil {
		s.show(w, v, http.StatusUnprocessableEntity, addView, pageData{Title: "Add a site", Error: "That didn't work: " + err.Error()})
		return
	}
	s.audit(r.Context(), v.session.Name, "source.added", created.Slug, s.client(r))
	http.Redirect(w, r, "/sources/"+strconv.FormatInt(created.ID, 10)+"/track", http.StatusSeeOther)
}
