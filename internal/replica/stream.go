// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package replica

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/pkg/client"
)

// maxEvent is the longest event the reader takes. A result carries a sample of
// entries for each candidate, which is well under this.
const maxEvent = 4 << 20

// Stage is a step the catalogue has started on while looking at a site.
type Stage = catalogue.DiscoveryStage

// DiscoverStages works out how to read a site, as Discover does, and calls stage
// with each step as the catalogue starts on it. stage runs on the caller's
// goroutine, before DiscoverStages returns.
func (c *Client) DiscoverStages(ctx context.Context, request client.DiscoveryRequest, stage func(Stage)) (client.DiscoveryResult, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return client.DiscoveryResult{}, fmt.Errorf("encode discovery: %w", err)
	}
	resp, err := c.api.CreateDiscoveryWithBody(ctx, "application/json", bytes.NewReader(body),
		func(_ context.Context, r *http.Request) error {
			r.Header.Set("Accept", "text/event-stream")
			return nil
		})
	if err != nil {
		return client.DiscoveryResult{}, down(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxEvent))
		return client.DiscoveryResult{}, failure(resp.StatusCode, b)
	}

	var result client.DiscoveryResult
	if t, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); t != "text/event-stream" {
		// A catalogue that doesn't stream answers all at once.
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxEvent)).Decode(&result); err != nil {
			return client.DiscoveryResult{}, fmt.Errorf("decode discovery: %w", err)
		}
		return result, nil
	}

	err = readEvents(resp.Body, func(name string, data []byte) (bool, error) {
		switch name {
		case "stage":
			var s Stage
			if err := json.Unmarshal(data, &s); err != nil {
				return false, fmt.Errorf("decode stage: %w", err)
			}
			if stage != nil {
				stage(s)
			}
			return false, nil
		case "result":
			if err := json.Unmarshal(data, &result); err != nil {
				return false, fmt.Errorf("decode discovery: %w", err)
			}
			return true, nil
		case "error":
			var p httpapi.Problem
			if err := json.Unmarshal(data, &p); err != nil || p.Status == 0 {
				return false, down(errors.New("the discovery failed without saying why"))
			}
			return false, &httpapi.Error{Problem: p}
		}
		return false, nil // An event this reader doesn't know is for a newer one.
	})
	if err != nil {
		return client.DiscoveryResult{}, err
	}
	return result, nil
}

// readEvents reads Server-Sent Events, as the HTML standard describes them, and
// hands each to handle until it says it's done. Only event and data fields matter
// here, so id and retry are skipped, as are comments.
func readEvents(r io.Reader, handle func(name string, data []byte) (bool, error)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEvent)
	var (
		name string
		data bytes.Buffer
	)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if data.Len() > 0 {
				if name == "" {
					name = "message"
				}
				done, err := handle(name, bytes.TrimSuffix(data.Bytes(), []byte("\n")))
				if err != nil || done {
					return err
				}
			}
			name = ""
			data.Reset()
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return down(fmt.Errorf("read the discovery's events: %w", err))
	}
	return down(errors.New("the discovery's events ended without an answer"))
}
