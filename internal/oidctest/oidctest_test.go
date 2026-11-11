// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package oidctest is a small OpenID provider for tests. It does the authorization
// code flow with PKCE, logs in whoever the test says without asking, and signs ID
// tokens with a key of its own.
package oidctest

import (
	"net/http"
	"net/url"
	"testing"
)

func TestOnlyTheRegisteredRedirect(t *testing.T) {
	p := New(t)
	for _, redirectURI := range []string{
		"https://attacker.example/login/oidc/callback",
		"https://andante.example/elsewhere",
		"https://andante.example/login/oidc/callback?next=https://attacker.example",
	} {
		q := url.Values{
			"client_id":             {p.ClientID},
			"response_type":         {"code"},
			"code_challenge_method": {"S256"},
			"code_challenge":        {"challenge"},
			"redirect_uri":          {redirectURI},
		}
		resp, err := p.Client().Get(p.Issuer() + "/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("redirect_uri %q: status %d, want %d", redirectURI, resp.StatusCode, http.StatusBadRequest)
		}
	}
}
