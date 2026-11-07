// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package clientip works out where a request really came from. Rate limits and the
// audit log both depend on it, so X-Forwarded-For only counts when it comes from a
// proxy that's been named.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver finds a request's address.
type Resolver struct {
	trusted []netip.Prefix
}

// New returns a Resolver that believes X-Forwarded-For from these CIDRs.
func New(cidrs []string) (*Resolver, error) {
	r := &Resolver{}
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", cidr, err)
		}
		r.trusted = append(r.trusted, prefix)
	}
	return r, nil
}

func (r *Resolver) isTrusted(addr netip.Addr) bool {
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// IP returns the client's address. It walks X-Forwarded-For from the right, because
// the left is whatever the client said, and stops at the first address that isn't a
// trusted proxy.
func (r *Resolver) IP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	current, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	current = current.Unmap()
	if !r.isTrusted(current) {
		return current.String()
	}
	hops := strings.Split(strings.Join(req.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		current = hop.Unmap()
		if !r.isTrusted(current) {
			break
		}
	}
	return current.String()
}
