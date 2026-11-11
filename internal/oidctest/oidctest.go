// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package oidctest is a small OpenID provider for tests. It does the authorization
// code flow with PKCE, logs in whoever the test says without asking, and signs ID
// tokens with a key of its own.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Identity is who the provider says is logging in.
type Identity struct {
	Subject           string   `json:"sub"`
	Email             string   `json:"email,omitempty"`
	EmailVerified     bool     `json:"email_verified,omitempty"`
	PreferredUsername string   `json:"preferred_username,omitempty"`
	Groups            []string `json:"groups,omitempty"`
}

// Provider is a running provider.
type Provider struct {
	ClientID, ClientSecret string
	// RedirectURI is the one callback the client is registered with, as at a real provider.
	RedirectURI string

	server *httptest.Server
	key    *rsa.PrivateKey
	signer jose.Signer

	mu    sync.Mutex
	next  Identity
	nonce *string
	codes map[string]grant
}

type grant struct {
	who                        Identity
	nonce, challenge, redirect string
}

// New starts a provider over TLS, and stops it when the test ends.
func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{ClientID: "andante", ClientSecret: "shh", RedirectURI: "https://andante.example/login/oidc/callback", key: key, signer: signer, codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	p.server = httptest.NewTLSServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

// Issuer is the provider's address.
func (p *Provider) Issuer() string { return p.server.URL }

// Client trusts the provider's certificate.
func (p *Provider) Client() *http.Client { return p.server.Client() }

// LogIn sets who the next login is.
func (p *Provider) LogIn(who Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = who
}

// SetNonce makes ID tokens carry this nonce, in place of the one they were asked for.
func (p *Provider) SetNonce(nonce string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nonce = &nonce
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.Issuer(),
		"authorization_endpoint":                p.Issuer() + "/authorize",
		"token_endpoint":                        p.Issuer() + "/token",
		"jwks_uri":                              p.Issuer() + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	if q.Get("redirect_uri") != p.RedirectURI {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	p.mu.Lock()
	nonce := q.Get("nonce")
	if p.nonce != nil {
		nonce = *p.nonce
	}
	p.codes[code] = grant{who: p.next, nonce: nonce, challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	p.mu.Unlock()
	v := url.Values{}
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	http.Redirect(w, r, p.RedirectURI+"?"+v.Encode(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	p.mu.Lock()
	g, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.PostForm.Get("redirect_uri") != g.redirect {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	claims, err := json.Marshal(g.who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var all map[string]any
	if err := json.Unmarshal(claims, &all); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	all["iss"], all["aud"], all["nonce"] = p.Issuer(), p.ClientID, g.nonce
	all["iat"], all["exp"] = now.Unix(), now.Add(time.Hour).Unix()
	payload, err := json.Marshal(all)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	signed, err := p.signer.Sign(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idToken, err := signed.CompactSerialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": rand.Text(), "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
