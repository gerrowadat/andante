// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
)

func TestAFailureSaysWhatToDo(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a missing page":   {&discovery.StatusError{URL: "https://example.com/x", Status: 404}, "There's no page at that address"},
		"a gone page":      {&discovery.StatusError{URL: "https://example.com/x", Status: 410}, "There's no page at that address"},
		"a refusal":        {fmt.Errorf("fetch: %w", &discovery.StatusError{URL: "https://example.com/x", Status: 403}), "may need a login"},
		"a broken site":    {&discovery.StatusError{URL: "https://example.com/x", Status: 502}, "having trouble"},
		"a backoff":        {&discovery.BackoffError{Host: "example.com", Until: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), Cause: "429"}, "Try again after 2026-11-16T12:00:00Z"},
		"no such host":     {&net.DNSError{Err: "no such host", Name: "exmaple.com", IsNotFound: true}, "Check the address for typos"},
		"robots.txt":       {fmt.Errorf("%w: https://example.com/", discovery.ErrDisallowed), "robots.txt asks"},
		"a private place":  {discovery.ErrAddress, "public internet"},
		"another scheme":   {discovery.ErrScheme, "http:// or https://"},
		"redirects":        {discovery.ErrTooManyHops, "the one it ends up at"},
		"too big":          {fmt.Errorf("%w: https://example.com/", discovery.ErrBodyTooLarge), "a single page or a feed"},
		"a cycle":          {fmt.Errorf("%w: back at https://example.com/1", discovery.ErrCycle), "starting from the first page"},
		"a slow site":      {fmt.Errorf("fetch: %w", context.DeadlineExceeded), "took too long"},
		"nothing to offer": {errors.New("the feed has no items"), ""},
	} {
		got := failure(tc.err)
		if !strings.HasPrefix(got, tc.err.Error()) {
			t.Errorf("%s: %q doesn't start with what went wrong", name, got)
		}
		if tc.want == "" {
			if got != tc.err.Error() {
				t.Errorf("%s: %q has a hint, and there's none to give", name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q doesn't say %q", name, got, tc.want)
		}
	}
	if got := statusHint(418); got != "" {
		t.Errorf("a status with nothing to suggest: %q", got)
	}
}
