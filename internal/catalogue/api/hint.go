// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
)

// failure says what went wrong reading a site and, where andante can tell, what to
// try instead. It's the detail of a probe-failed problem, which a person reads.
func failure(err error) string {
	if hint := hintFor(err); hint != "" {
		return err.Error() + ". " + hint
	}
	return err.Error()
}

func hintFor(err error) string {
	var (
		status  *discovery.StatusError
		backoff *discovery.BackoffError
		dns     *net.DNSError
	)
	switch {
	case errors.As(err, &status):
		return statusHint(status.Status)
	case errors.As(err, &backoff):
		return "The site asked andante to slow down, or is failing. Try again after " +
			backoff.Until.UTC().Format(time.RFC3339) + "."
	case errors.As(err, &dns):
		return "There's no site by that name. Check the address for typos."
	case errors.Is(err, discovery.ErrDisallowed):
		return "The site's robots.txt asks automated readers to stay out of that page, and andante does as it asks."
	case errors.Is(err, discovery.ErrAddress):
		return "andante only reads sites on the public internet."
	case errors.Is(err, discovery.ErrScheme):
		return "Use an address that starts with http:// or https://."
	case errors.Is(err, discovery.ErrTooManyHops):
		return "Open the address in a browser and use the one it ends up at."
	case errors.Is(err, discovery.ErrBodyTooLarge):
		return "Try the address of a single page or a feed instead."
	case errors.Is(err, discovery.ErrCycle):
		return "Following each page's next link leads back to an earlier page. Try starting from the first page instead."
	case errors.Is(err, context.DeadlineExceeded):
		return "The site took too long to answer. Try again later."
	}
	return ""
}

func statusHint(status int) string {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return "There's no page at that address. Check it in a browser."
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "The site turned andante away. It may need a login, or block automated readers."
	case status >= http.StatusInternalServerError:
		return "The site is having trouble. Try again later."
	}
	return ""
}
