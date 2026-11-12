// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package bootstrap makes the first admin from a bootstrap token, so that setting up a
// server needs a browser and its logs, and not a shell on it. The token comes from
// --bootstrap-token, or the server makes one and logs it. Either stops working once
// there's an active admin. The website's /setup and the API's POST /bootstrap both
// come here.
package bootstrap

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/throttle"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// throttleName is what the throttle counts guesses at the token against, as if it
// were an account.
const throttleName = "\x00bootstrap"

// Errors a caller turns into its own kind of refusal. A name that's taken is
// store.ErrConflict, and one that's no good is store.ErrBadName.
var (
	// ErrClosed means there's nothing to set up: there's an admin, or no token.
	ErrClosed = errors.New("there's nothing to set up")
	// ErrWrongToken means the token given isn't the bootstrap token.
	ErrWrongToken = errors.New("that isn't the bootstrap token")
)

// ThrottledError means this address has guessed wrong too often.
type ThrottledError struct{ Wait time.Duration }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("too many tries; wait %s", e.Wait.Round(time.Second))
}

// Options configure the service.
type Options struct {
	Store *store.Store
	// Token is --bootstrap-token. Empty means the server makes one.
	Token    string
	Throttle *throttle.Throttle
	BaseURL  string
	Registry prometheus.Registerer
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service makes the first admin.
type Service struct {
	opts     Options
	attempts *prometheus.CounterVec
}

// New returns the service.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Throttle == nil {
		opts.Throttle = throttle.New()
	}
	return &Service{
		opts: opts,
		attempts: promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_bootstrap_attempts_total",
			Help: "Tries at the bootstrap token, by result: ok, wrong, throttled, closed or refused (the name was no good).",
		}, []string{"result"}),
	}
}

// SetupURL is where someone goes to use the token.
func (s *Service) SetupURL() string {
	return strings.TrimRight(s.opts.BaseURL, "/") + "/setup"
}

// Announce runs as the server starts. While there's no active admin it logs where to
// go, and the token too if the server made it. Once there's an admin it deletes any
// token the server made.
func (s *Service) Announce(ctx context.Context) error {
	if s.opts.Token != "" {
		if err := s.opts.Store.ForgetBootstrapToken(ctx); err != nil {
			return err
		}
		needs, err := s.opts.Store.NeedsAdmin(ctx)
		switch {
		case err != nil:
			return err
		case needs:
			s.opts.Logger.Warn("there's no admin yet; go to the setup page and give it the token from --bootstrap-token", "url", s.SetupURL())
		default:
			s.opts.Logger.Info("there's an admin, so --bootstrap-token can come out")
		}
		return nil
	}
	raw, until, err := s.opts.Store.BootstrapToken(ctx, s.opts.Now())
	switch {
	case errors.Is(err, store.ErrHaveAdmin):
		return nil
	case err != nil:
		return err
	}
	s.opts.Logger.Warn("there's no admin yet; go to the setup page and give it the bootstrap token",
		"url", s.SetupURL(), "token", raw, "until", until.UTC().Format(time.RFC3339))
	return nil
}

// Open reports whether there's anything to set up: no active admin, and a token to do
// it with.
func (s *Service) Open(ctx context.Context) (bool, error) {
	_, err := s.token(ctx)
	if errors.Is(err, ErrClosed) {
		return false, nil
	}
	return err == nil, err
}

// token returns the bootstrap token, or ErrClosed if there's none or there's an admin.
func (s *Service) token(ctx context.Context) (string, error) {
	needs, err := s.opts.Store.NeedsAdmin(ctx)
	if err != nil {
		return "", err
	}
	if !needs {
		return "", ErrClosed
	}
	if s.opts.Token != "" {
		return s.opts.Token, nil
	}
	raw, err := s.opts.Store.StoredBootstrapToken(ctx, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrClosed
	}
	return raw, err
}

// check checks a token, throttled by address, and audits a wrong one.
func (s *Service) check(ctx context.Context, given, name, ip string) error {
	now := s.opts.Now()
	if wait := s.opts.Throttle.Wait(ip, throttleName, now); wait > 0 {
		s.attempts.WithLabelValues("throttled").Inc()
		return &ThrottledError{Wait: wait}
	}
	want, err := s.token(ctx)
	if errors.Is(err, ErrClosed) {
		s.attempts.WithLabelValues("closed").Inc()
	}
	if err != nil {
		return err
	}
	// Comparing hashes keeps the comparison the same length, and so the same time,
	// whatever was given.
	a, b := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(given))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		s.opts.Throttle.Failed(ip, throttleName, now)
		s.attempts.WithLabelValues("wrong").Inc()
		s.audit(ctx, name, "bootstrap.failed", ip)
		return ErrWrongToken
	}
	s.opts.Throttle.Succeeded(ip, throttleName)
	return nil
}

// done counts and audits the end of a try with the right token.
func (s *Service) done(ctx context.Context, name, ip string, err error) {
	if err != nil {
		s.attempts.WithLabelValues("refused").Inc()
		return
	}
	s.attempts.WithLabelValues("ok").Inc()
	s.audit(ctx, name, "bootstrap", ip)
}

func (s *Service) audit(ctx context.Context, name, action, ip string) {
	entry := store.AuditEntry{Who: "bootstrap token", Action: action, Target: name, IP: ip}
	if err := s.opts.Store.Audit(ctx, entry, s.opts.Now()); err != nil {
		s.opts.Logger.Error("could not write the audit log", "error", err)
	}
}

// Invite checks the token and makes an invite for a new admin called name. It returns
// the invite's raw token, for /invite/{token}, where the person picks a password or
// signs in with the provider.
func (s *Service) Invite(ctx context.Context, given, name, ip string) (string, error) {
	if err := s.check(ctx, given, name, ip); err != nil {
		return "", err
	}
	_, raw, err := s.opts.Store.CreateBootstrapInvite(ctx, name, s.opts.Now())
	if errors.Is(err, store.ErrHaveAdmin) {
		err = ErrClosed
	}
	s.done(ctx, name, ip, err)
	return raw, err
}

// Admin is what Make returns.
type Admin struct {
	User db.User
	// APIToken is the new admin's first API token.
	APIToken string
	// Invite is the raw token of a link where they can set a password.
	Invite string
}

// Make checks the token and makes a new admin called name straight away, for a site
// with no browser in front of it.
func (s *Service) Make(ctx context.Context, given, name, ip string) (Admin, error) {
	if err := s.check(ctx, given, name, ip); err != nil {
		return Admin{}, err
	}
	user, token, err := s.opts.Store.CreateBootstrapAdmin(ctx, name)
	if errors.Is(err, store.ErrHaveAdmin) {
		err = ErrClosed
	}
	s.done(ctx, name, ip, err)
	if err != nil {
		return Admin{}, err
	}
	// The admin exists either way. Without the link they can still get one with
	// their API token.
	_, invite, err := s.opts.Store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: name, Reset: true}, s.opts.Now())
	if err != nil {
		s.opts.Logger.Warn("could not make a password link for the first admin", "error", err)
	}
	return Admin{User: user, APIToken: token, Invite: invite}, nil
}
