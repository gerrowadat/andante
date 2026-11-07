// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gerrowadat/andante/internal/clientip"
)

func TestIP(t *testing.T) {
	r, err := clientip.New([]string{"10.0.0.0/8", "fd00::/8"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remote string
		forwarded    []string
		want         string
	}{
		{"direct", "203.0.113.9:1234", nil, "203.0.113.9"},
		{"a stranger can't claim an address", "203.0.113.9:1234", []string{"198.51.100.1"}, "203.0.113.9"},
		{"through a proxy", "10.0.0.1:1234", []string{"198.51.100.1"}, "198.51.100.1"},
		{"through two proxies", "10.0.0.1:1234", []string{"198.51.100.1, 10.0.0.2"}, "198.51.100.1"},
		{"a forged left side", "10.0.0.1:1234", []string{"1.2.3.4, 198.51.100.1"}, "198.51.100.1"},
		{"two headers", "10.0.0.1:1234", []string{"1.2.3.4", "198.51.100.1"}, "198.51.100.1"},
		{"a proxy with no header", "10.0.0.1:1234", nil, "10.0.0.1"},
		{"nonsense from a proxy", "10.0.0.1:1234", []string{"not-an-address"}, "10.0.0.1"},
		{"all proxies", "10.0.0.1:1234", []string{"10.0.0.3, 10.0.0.2"}, "10.0.0.3"},
		{"IPv6", "[fd00::1]:1234", []string{"2001:db8::1"}, "2001:db8::1"},
		{"IPv4 in IPv6", "[::ffff:10.0.0.1]:1234", []string{"198.51.100.1"}, "198.51.100.1"},
		{"no port", "203.0.113.9", nil, "203.0.113.9"},
		{"not an address", "somewhere", nil, "somewhere"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			for _, v := range tt.forwarded {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := r.IP(req); got != tt.want {
				t.Errorf("IP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBadCIDR(t *testing.T) {
	if _, err := clientip.New([]string{"not-a-cidr"}); err == nil {
		t.Error("accepted a bad CIDR")
	}
}
