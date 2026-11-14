// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package replica_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/pkg/client"
)

func TestDiscoverStagesFromTheCatalogue(t *testing.T) {
	s := stacktest.New(t)
	site := sitetest.Serve(t, "discover-comic")
	var steps []string
	result, err := s.Client.DiscoverStages(t.Context(), client.DiscoveryRequest{URL: site.URL + "/"}, func(st replica.Stage) {
		steps = append(steps, string(st.Step))
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Title != "The Comic" {
		t.Errorf("candidates = %+v", result.Candidates)
	}
	if len(steps) < 3 || steps[0] != "fetch" || steps[len(steps)-1] != "probe" {
		t.Errorf("steps = %v", steps)
	}

	// A site that isn't there is a problem from the stream, after the first stage.
	steps = nil
	_, err = s.Client.DiscoverStages(t.Context(), client.DiscoveryRequest{URL: site.URL + "/gone"}, func(st replica.Stage) {
		steps = append(steps, string(st.Step))
	})
	var problem *httpapi.Error
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusUnprocessableEntity {
		t.Errorf("err = %v", err)
	}
	if len(steps) != 1 {
		t.Errorf("steps = %v", steps)
	}
}

// fake is a catalogue that answers a discovery with whatever it's given.
func fake(t *testing.T, status int, contentType, body string) *replica.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := replica.NewClient(srv.URL, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDiscoverStagesReadsTheStream(t *testing.T) {
	c := fake(t, http.StatusOK, "text/event-stream; charset=utf-8", ": a comment\r\n\r\n"+
		"event: stage\ndata: {\"step\":\"fetch\",\ndata: \"message\":\"one\"}\n\n"+
		"event: something-new\ndata: {}\n\n"+
		"data: an unnamed one\n\n"+
		"event: result\ndata: {\"candidates\":[{\"title\":\"T\",\"default_kind\":\"page\",\"home_url\":\"h\",\"config\":{\"strategy\":\"feed\",\"feed_url\":\"f\"},\"sample\":[],\"dated_share\":0,\"warnings\":[],\"similar_source_ids\":[]}]}\n\n"+
		"event: stage\ndata: {\"step\":\"probe\",\"message\":\"after the end\"}\n\n")
	var stages []replica.Stage
	result, err := c.DiscoverStages(t.Context(), client.DiscoveryRequest{URL: "https://a.example/"}, func(s replica.Stage) {
		stages = append(stages, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 1 || stages[0].Step != "fetch" || stages[0].Message != "one" {
		t.Errorf("stages = %+v", stages)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Title != "T" {
		t.Errorf("result = %+v", result)
	}
}

func TestDiscoverStagesFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		status      int
		contentType string
		body        string
		wantStatus  int
	}{
		"an error event":                   {http.StatusOK, "text/event-stream", "event: error\ndata: {\"type\":\"about:blank\",\"title\":\"Nope\",\"status\":422,\"detail\":\"no\"}\n\n", http.StatusUnprocessableEntity},
		"an error event that says nothing": {http.StatusOK, "text/event-stream", "event: error\ndata: {}\n\n", http.StatusServiceUnavailable},
		"a stream that ends early":         {http.StatusOK, "text/event-stream", "event: stage\ndata: {\"step\":\"fetch\",\"message\":\"m\"}\n\n", http.StatusServiceUnavailable},
		"a refusal":                        {http.StatusUnprocessableEntity, "application/problem+json", "{\"type\":\"about:blank\",\"title\":\"Bad\",\"status\":422}", http.StatusUnprocessableEntity},
		"a bad stage":                      {http.StatusOK, "text/event-stream", "event: stage\ndata: [\n\n", 0},
	} {
		t.Run(name, func(t *testing.T) {
			c := fake(t, tc.status, tc.contentType, tc.body)
			_, err := c.DiscoverStages(t.Context(), client.DiscoveryRequest{URL: "https://a.example/"}, nil)
			if err == nil {
				t.Fatal("no error")
			}
			var problem *httpapi.Error
			if tc.wantStatus != 0 && (!errors.As(err, &problem) || int(problem.Problem.Status) != tc.wantStatus) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestDiscoverStagesFromACatalogueThatDoesntStream(t *testing.T) {
	c := fake(t, http.StatusOK, "application/json", `{"candidates":[]}`)
	result, err := c.DiscoverStages(t.Context(), client.DiscoveryRequest{URL: "https://a.example/"}, nil)
	if err != nil || len(result.Candidates) != 0 {
		t.Errorf("result = %+v, %v", result, err)
	}
}
