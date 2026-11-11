// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gerrowadat/andante/internal/store"
	"golang.org/x/oauth2"
)

// OIDCOptions turn on logging in with an OpenID provider.
type OIDCOptions struct {
	Issuer, ClientID, ClientSecret string
	// Someone the provider vouches for gets an account without an invite if their
	// verified email is in one of AllowedDomains, or they're in one of AllowedGroups.
	AllowedDomains, AllowedGroups []string
	// HTTPClient talks to the provider. Nil is a client with a timeout.
	HTTPClient *http.Client
}

const (
	callbackPath = "/login/oidc/callback"
	oidcTimeout  = 15 * time.Second
)

// oidcLogin finds the provider the first time someone uses it, so that a provider
// that's down doesn't stop the server starting.
type oidcLogin struct {
	opts     OIDCOptions
	redirect string

	mu       sync.Mutex
	provider *oidc.Provider
}

func newOIDCLogin(opts OIDCOptions, baseURL string) *oidcLogin {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: oidcTimeout}
	}
	return &oidcLogin{opts: opts, redirect: strings.TrimRight(baseURL, "/") + callbackPath}
}

func (o *oidcLogin) context(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, o.opts.HTTPClient)
}

func (o *oidcLogin) setup() (*oidc.Provider, *oauth2.Config, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider == nil {
		// Not the request's context: the provider keeps it for fetching keys later.
		p, err := oidc.NewProvider(o.context(context.Background()), o.opts.Issuer)
		if err != nil {
			return nil, nil, fmt.Errorf("find the OpenID provider: %w", err)
		}
		o.provider = p
	}
	scopes := []string{oidc.ScopeOpenID, "email", "profile"}
	if len(o.opts.AllowedGroups) > 0 {
		scopes = append(scopes, "groups")
	}
	return o.provider, &oauth2.Config{
		ClientID: o.opts.ClientID, ClientSecret: o.opts.ClientSecret, RedirectURL: o.redirect,
		Endpoint: o.provider.Endpoint(), Scopes: scopes,
	}, nil
}

// claims are the parts of an ID token andante looks at. Only the issuer and subject
// say who someone is. The rest only decides whether a stranger may join, and what to
// call them.
type claims struct {
	Email             string   `json:"email"`
	EmailVerified     bool     `json:"email_verified"`
	PreferredUsername string   `json:"preferred_username"`
	Groups            []string `json:"groups"`
}

func (o *oidcLogin) allowed(c claims) bool {
	if c.EmailVerified {
		if at := strings.LastIndexByte(c.Email, '@'); at >= 0 {
			domain := strings.ToLower(c.Email[at+1:])
			if slices.ContainsFunc(o.opts.AllowedDomains, func(d string) bool { return strings.EqualFold(d, domain) }) {
				return true
			}
		}
	}
	for _, g := range c.Groups {
		if slices.Contains(o.opts.AllowedGroups, g) {
			return true
		}
	}
	return false
}

var notInName = regexp.MustCompile(`[^a-z0-9._-]+`)

// nameFor makes a user name from what the provider says. It's only a starting point:
// if it's taken, the new user gets a number on the end.
func nameFor(c claims) string {
	name := c.PreferredUsername
	if name == "" {
		name, _, _ = strings.Cut(c.Email, "@")
	}
	name = notInName.ReplaceAllString(strings.ToLower(name), "-")
	name = strings.TrimLeft(name, "._-")
	if len(name) > maxNameLength {
		name = name[:maxNameLength]
	}
	if name == "" {
		return "user"
	}
	return name
}

// oidcFlow is what the callback needs to check that it's finishing the login this
// browser started.
type oidcFlow struct {
	State, Nonce, Verifier string
	// Invite is the raw invite token, when the login is to use one.
	Invite string
	Until  time.Time
}

func (f *oidcFlow) until() time.Time { return f.Until }

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *site) startOIDC(w http.ResponseWriter, r *http.Request, invite string) {
	_, cfg, err := s.oidc.setup()
	if err != nil {
		s.opts.Logger.Warn("could not reach the OpenID provider", "error", err)
		s.oidcLogins.WithLabelValues("failed").Inc()
		s.render(w, http.StatusServiceUnavailable, loginPage, s.loginData("The login provider can't be reached. Try again in a moment."))
		return
	}
	flow := oidcFlow{Verifier: oauth2.GenerateVerifier(), Invite: invite, Until: s.opts.Now().Add(pendingLife)}
	if flow.State, err = random(); err == nil {
		flow.Nonce, err = random()
	}
	if err == nil {
		err = s.setSealed(w, oidcCookie, "oidc", &flow)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, cfg.AuthCodeURL(flow.State, oidc.Nonce(flow.Nonce), oauth2.S256ChallengeOption(flow.Verifier)), http.StatusSeeOther)
}

func (s *site) loginWithOIDC(w http.ResponseWriter, r *http.Request) {
	s.startOIDC(w, r, "")
}

func (s *site) inviteWithOIDC(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	if _, err := s.opts.Store.LookupInvite(r.Context(), raw, s.opts.Now()); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.fail(w, err)
			return
		}
		http.NotFound(w, r)
		return
	}
	s.startOIDC(w, r, raw)
}

// verify finishes the provider's side of a login and says who it was.
func (s *site) verify(r *http.Request, flow oidcFlow) (store.Identity, claims, error) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return store.Identity{}, claims{}, fmt.Errorf("the provider said %q", e)
	}
	if !hmac.Equal([]byte(q.Get("state")), []byte(flow.State)) {
		return store.Identity{}, claims{}, errors.New("the state doesn't match")
	}
	provider, cfg, err := s.oidc.setup()
	if err != nil {
		return store.Identity{}, claims{}, err
	}
	ctx := s.oidc.context(r.Context())
	token, err := cfg.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return store.Identity{}, claims{}, fmt.Errorf("exchange the code: %w", err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return store.Identity{}, claims{}, errors.New("no ID token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID, Now: s.opts.Now}).Verify(ctx, raw)
	if err != nil {
		return store.Identity{}, claims{}, fmt.Errorf("verify the ID token: %w", err)
	}
	if !hmac.Equal([]byte(idToken.Nonce), []byte(flow.Nonce)) {
		return store.Identity{}, claims{}, errors.New("the nonce doesn't match")
	}
	var c claims
	if err := idToken.Claims(&c); err != nil {
		return store.Identity{}, claims{}, fmt.Errorf("read the claims: %w", err)
	}
	return store.Identity{Issuer: idToken.Issuer, Subject: idToken.Subject}, c, nil
}

func (s *site) oidcCallback(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, result, message string) {
		s.oidcLogins.WithLabelValues(result).Inc()
		s.render(w, status, loginPage, s.loginData(message))
	}
	var flow oidcFlow
	if !s.openSealed(r, oidcCookie, "oidc", &flow) {
		refuse(http.StatusBadRequest, "failed", "That login took too long, or was started somewhere else. Try again.")
		return
	}
	s.clearSealed(w, oidcCookie)
	id, c, err := s.verify(r, flow)
	if err != nil {
		s.opts.Logger.Info("an OpenID login failed", "error", err)
		refuse(http.StatusBadRequest, "failed", "The login provider didn't log you in. Try again.")
		return
	}

	ctx, client, now := r.Context(), s.client(r), s.opts.Now()
	if err := s.endOldSession(r); err != nil {
		s.fail(w, err)
		return
	}
	var (
		user    string
		session string
		result  = "ok"
	)
	if flow.Invite != "" {
		u, raw, err := s.opts.Store.RedeemInviteWithIdentity(ctx, flow.Invite, id, client, now)
		switch {
		case errors.Is(err, store.ErrNotFound):
			refuse(http.StatusNotFound, "refused", "That invite has been used, or it's run out.")
			return
		case errors.Is(err, store.ErrConflict):
			refuse(http.StatusConflict, "refused", "That login already belongs to someone here. Log in with it instead.")
			return
		case err != nil:
			s.fail(w, err)
			return
		}
		s.audit(ctx, u.Name, "invite.used", u.Name, client)
		user, session, result = u.Name, raw, "linked"
	} else {
		u, raw, err := s.opts.Store.LoginByIdentity(ctx, id, client, now)
		switch {
		case errors.Is(err, store.ErrNotFound) && s.oidc.allowed(c):
			u, raw, err = s.opts.Store.CreateIdentityUser(ctx, nameFor(c), id, client, now)
			if err != nil {
				s.fail(w, err)
				return
			}
			s.audit(ctx, u.Name, "user.created", u.Name, client)
			result = "created"
		case errors.Is(err, store.ErrNotFound):
			refuse(http.StatusForbidden, "refused", "There's no account here for that login. Ask an admin for an invite.")
			return
		case errors.Is(err, store.ErrDisabled):
			s.audit(ctx, u.Name, "login.failed", "oidc", client)
			refuse(http.StatusForbidden, "refused", "That account can't log in.")
			return
		case err != nil:
			s.fail(w, err)
			return
		}
		user, session = u.Name, raw
	}
	s.oidcLogins.WithLabelValues(result).Inc()
	s.logins.WithLabelValues("ok").Inc()
	s.audit(ctx, user, "login", "oidc", client)
	s.setSession(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
