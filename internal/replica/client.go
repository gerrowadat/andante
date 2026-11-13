// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package replica is andante-server's side of the catalogue: a client for its API,
// and the sync that keeps a local copy of sources and entries and tells the
// catalogue how far readers have got.
package replica

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/pkg/client"
)

// Client talks to the catalogue. The two APIs' sources and probes have the same
// JSON, so bodies pass straight through. An error is an *httpapi.Error, with the
// catalogue's own problem where it gave one.
type Client struct {
	api *catalogue.ClientWithResponses
}

// NewClient returns a Client. There's no client-wide timeout, because probes and
// held change requests are long. Callers bound them with their contexts.
func NewClient(url, token string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	api, err := catalogue.NewClientWithResponses(url,
		catalogue.WithHTTPClient(httpClient),
		catalogue.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("create catalogue client: %w", err)
	}
	return &Client{api: api}, nil
}

func down(err error) error {
	return httpapi.NewProblem(http.StatusServiceUnavailable, httpapi.TypeCatalogueDown, "the catalogue could not be reached: "+err.Error())
}

// failure turns a response the caller didn't want into an error.
func failure(status int, body []byte) error {
	var p httpapi.Problem
	if err := json.Unmarshal(body, &p); err != nil || p.Status == 0 {
		return down(fmt.Errorf("unexpected status %d", status))
	}
	return &httpapi.Error{Problem: p}
}

// SourceCreate is a new source, and the state to create it in.
type SourceCreate struct {
	client.SourceCreate
	State string
}

// MarshalJSON adds the state to the API's body.
func (s SourceCreate) MarshalJSON() ([]byte, error) {
	body, err := json.Marshal(s.SourceCreate)
	if err != nil || s.State == "" {
		return body, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	if fields["state"], err = json.Marshal(s.State); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// CreateSource creates a source.
func (c *Client) CreateSource(ctx context.Context, source SourceCreate) (client.Source, error) {
	body, err := json.Marshal(source)
	if err != nil {
		return client.Source{}, fmt.Errorf("encode source: %w", err)
	}
	resp, err := c.api.CreateSourceWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body))
	if err != nil {
		return client.Source{}, down(err)
	}
	if resp.StatusCode() != http.StatusCreated {
		return client.Source{}, failure(resp.StatusCode(), resp.Body)
	}
	var created client.Source
	if err := json.Unmarshal(resp.Body, &created); err != nil {
		return client.Source{}, fmt.Errorf("decode source: %w", err)
	}
	return created, nil
}

// UpdateSource applies a merge patch to a source.
func (c *Client) UpdateSource(ctx context.Context, id int64, patch client.SourcePatch) (client.Source, error) {
	body, err := json.Marshal(patch)
	if err != nil {
		return client.Source{}, fmt.Errorf("encode patch: %w", err)
	}
	resp, err := c.api.UpdateSourceWithBodyWithResponse(ctx, id, "application/merge-patch+json", bytes.NewReader(body))
	if err != nil {
		return client.Source{}, down(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return client.Source{}, failure(resp.StatusCode(), resp.Body)
	}
	var updated client.Source
	if err := json.Unmarshal(resp.Body, &updated); err != nil {
		return client.Source{}, fmt.Errorf("decode source: %w", err)
	}
	return updated, nil
}

// DeleteSource deletes a source.
func (c *Client) DeleteSource(ctx context.Context, id int64) error {
	resp, err := c.api.DeleteSourceWithResponse(ctx, id)
	if err != nil {
		return down(err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return failure(resp.StatusCode(), resp.Body)
	}
	return nil
}

// RequestCrawl asks for a crawl of a source.
func (c *Client) RequestCrawl(ctx context.Context, id int64) error {
	resp, err := c.api.RequestCrawlWithResponse(ctx, id)
	if err != nil {
		return down(err)
	}
	if resp.StatusCode() != http.StatusAccepted {
		return failure(resp.StatusCode(), resp.Body)
	}
	return nil
}

// Probe runs a probe.
func (c *Client) Probe(ctx context.Context, request client.ProbeRequest) (client.ProbeResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return client.ProbeResult{}, fmt.Errorf("encode probe: %w", err)
	}
	resp, err := c.api.CreateProbeWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body))
	if err != nil {
		return client.ProbeResult{}, down(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return client.ProbeResult{}, failure(resp.StatusCode(), resp.Body)
	}
	var result client.ProbeResult
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return client.ProbeResult{}, fmt.Errorf("decode probe: %w", err)
	}
	return result, nil
}

// Discover works out how to read a site.
func (c *Client) Discover(ctx context.Context, request client.DiscoveryRequest) (client.DiscoveryResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return client.DiscoveryResult{}, fmt.Errorf("encode discovery: %w", err)
	}
	resp, err := c.api.CreateDiscoveryWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body))
	if err != nil {
		return client.DiscoveryResult{}, down(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return client.DiscoveryResult{}, failure(resp.StatusCode(), resp.Body)
	}
	var result client.DiscoveryResult
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return client.DiscoveryResult{}, fmt.Errorf("decode discovery: %w", err)
	}
	return result, nil
}

// SetDemand says how far readers have got, or with nil that nobody is reading.
func (c *Client) SetDemand(ctx context.Context, id int64, seq *int32) error {
	if seq == nil {
		resp, err := c.api.ClearDemandWithResponse(ctx, id)
		if err != nil {
			return down(err)
		}
		if resp.StatusCode() != http.StatusNoContent {
			return failure(resp.StatusCode(), resp.Body)
		}
		return nil
	}
	resp, err := c.api.SetDemandWithResponse(ctx, id, catalogue.Demand{Seq: *seq})
	if err != nil {
		return down(err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return failure(resp.StatusCode(), resp.Body)
	}
	return nil
}

// Changes returns changes after a revision, waiting up to wait seconds for one.
func (c *Client) Changes(ctx context.Context, after int64, limit, wait int32) ([]catalogue.Change, error) {
	resp, err := c.api.ListChangesWithResponse(ctx, &catalogue.ListChangesParams{After: &after, Limit: &limit, Wait: &wait})
	if err != nil {
		return nil, down(err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, failure(resp.StatusCode(), resp.Body)
	}
	return resp.JSON200.Changes, nil
}

// Logs returns what the catalogue logged lately, newest first, and whether there's
// more past the limit.
func (c *Client) Logs(ctx context.Context, params catalogue.ListLogsParams) ([]client.LogRecord, bool, error) {
	resp, err := c.api.ListLogsWithResponse(ctx, &params)
	if err != nil {
		return nil, false, down(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, false, failure(resp.StatusCode(), resp.Body)
	}
	var list struct {
		Items []client.LogRecord `json:"items"`
		More  bool               `json:"more"`
	}
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return nil, false, fmt.Errorf("decode logs: %w", err)
	}
	return list.Items, list.More, nil
}

// Fetches returns a source's crawl log, newest first, and whether there's more past
// the limit.
func (c *Client) Fetches(ctx context.Context, id int64, params catalogue.ListFetchesParams) ([]client.Fetch, bool, error) {
	resp, err := c.api.ListFetchesWithResponse(ctx, id, &params)
	if err != nil {
		return nil, false, down(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, false, failure(resp.StatusCode(), resp.Body)
	}
	var list struct {
		Items []client.Fetch `json:"items"`
		More  bool           `json:"more"`
	}
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return nil, false, fmt.Errorf("decode fetches: %w", err)
	}
	return list.Items, list.More, nil
}
