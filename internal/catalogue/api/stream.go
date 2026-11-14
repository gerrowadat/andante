// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/catalogue/api/gen"
	"github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/find"
	"github.com/gerrowadat/andante/internal/httpapi"
)

const (
	eventStream = "text/event-stream"
	// streamBeat is how often a quiet stream gets a comment, so that nothing between
	// the two services decides it's idle. Stages can be a crawl interval apart.
	streamBeat = 15 * time.Second
)

type wantsStreamKey struct{}

// noteStream records whether a discovery asked for a stream. The strict handler only
// sees the body, and Accept isn't a parameter in the description.
func noteStream(next gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		if operationID == "CreateDiscovery" && accepts(r.Header.Get("Accept"), eventStream) {
			ctx = context.WithValue(ctx, wantsStreamKey{}, true)
		}
		return next(ctx, w, r, request)
	}
}

func wantsStream(ctx context.Context) bool {
	wants, _ := ctx.Value(wantsStreamKey{}).(bool)
	return wants
}

// accepts says whether an Accept header names a media type.
func accepts(header, mediaType string) bool {
	for _, part := range strings.Split(header, ",") {
		if t, _, err := mime.ParseMediaType(strings.TrimSpace(part)); err == nil && t == mediaType {
			return true
		}
	}
	return false
}

// streamDiscovery answers a discovery as Server-Sent Events: each stage as it starts,
// then the result or a problem. The finding runs as the response is written, and a
// pipe takes the events to the generated code, which flushes each one.
func (h *handler) streamDiscovery(ctx context.Context, address string, limit int) gen.CreateDiscoveryResponseObject {
	reader, writer := io.Pipe()
	go func() {
		ctx, cancel := context.WithTimeout(ctx, h.probeTimeout)
		defer cancel()
		// A pipe gates each Write, so the beat and the stages can't interleave.
		stopBeat := make(chan struct{})
		go beat(writer, stopBeat)
		defer close(stopBeat)

		result, err := h.discover(ctx, address, limit, func(s find.Stage) {
			_ = writeEvent(writer, "stage", client.DiscoveryStage{Step: client.DiscoveryStageStep(s.Step), Message: s.Message})
		})
		if err != nil {
			err = writeEvent(writer, "error", h.problemOf(ctx, err))
		} else {
			err = writeEvent(writer, "result", result)
		}
		writer.CloseWithError(err)
	}()
	return gen.CreateDiscovery200TextEventStreamResponse{Body: reader}
}

func beat(w io.Writer, stop <-chan struct{}) {
	ticker := time.NewTicker(streamBeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if _, err := io.WriteString(w, ": still looking\n\n"); err != nil {
				return
			}
		}
	}
}

// problemOf is what a failure says on a stream, where the status has already gone.
func (h *handler) problemOf(ctx context.Context, err error) httpapi.Problem {
	var p *httpapi.Error
	if errors.As(err, &p) {
		return p.Problem
	}
	h.logger.Error("API request failed", "request_id", httpapi.RequestID(ctx), "error", err)
	return httpapi.Problem{Type: httpapi.TypeBlank, Title: http.StatusText(http.StatusInternalServerError), Status: http.StatusInternalServerError}
}

// writeEvent writes one event. JSON has no newlines, so its data is one line.
func writeEvent(w io.Writer, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s event: %w", name, err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}
