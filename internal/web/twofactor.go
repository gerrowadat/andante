// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/twofactor"
)

// pending is someone who got their password right and still owes a code.
type pending struct {
	User  int64
	Name  string
	Until time.Time
}

func (p *pending) until() time.Time { return p.Until }

const wrongCode = "That code isn't right."

// checkCode takes either a code from an authenticator app or a recovery code, and uses
// it up, so that neither works twice.
func (s *site) checkCode(ctx context.Context, userID int64, given string) (bool, error) {
	given = strings.TrimSpace(given)
	if given == "" {
		return false, nil
	}
	if !twofactor.IsCode(given) {
		ok, err := s.opts.Store.UseRecoveryCode(ctx, userID, given, s.opts.Now())
		if ok {
			s.codes.WithLabelValues("recovery").Inc()
		}
		return ok, err
	}
	totp, err := s.opts.Store.GetTOTP(ctx, userID)
	if err != nil || !totp.Enabled {
		return false, err
	}
	step, ok := twofactor.Check(totp.Secret, given, s.opts.Now())
	if !ok {
		return false, nil
	}
	fresh, err := s.opts.Store.UseTOTPStep(ctx, userID, step)
	if err == nil && !fresh {
		s.codes.WithLabelValues("reused").Inc()
	}
	if fresh {
		s.codes.WithLabelValues("ok").Inc()
	}
	return fresh, err
}

func (s *site) showCode(w http.ResponseWriter, r *http.Request) {
	var p pending
	if !s.openSealed(r, pendingCookie, "pending", &p) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, codePage, map[string]string{})
}

func (s *site) enterCode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	var p pending
	if !s.openSealed(r, pendingCookie, "pending", &p) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx, client, now := r.Context(), s.client(r), s.opts.Now()
	if wait := s.throttle.wait(client.IP, p.Name, now); wait > 0 {
		s.tooMany(w, codePage, wait)
		return
	}
	ok, err := s.checkCode(ctx, p.User, r.PostForm.Get("code"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.codes.WithLabelValues("wrong").Inc()
		s.throttle.failed(client.IP, p.Name, now)
		s.logins.WithLabelValues("failed").Inc()
		s.audit(ctx, p.Name, "login.code.failed", "", client)
		s.render(w, http.StatusUnauthorized, codePage, map[string]string{"Error": wrongCode})
		return
	}
	s.clearSealed(w, pendingCookie)
	s.startSession(w, r, p.User, p.Name)
}

type twoFactorData struct {
	Secret string
	// URL is otpauth:, which html/template would otherwise refuse to put in a link.
	URL template.URL
}

func (s *site) showTwoFactorSetup(w http.ResponseWriter, v visitor, status int, secret, problem string) {
	u, err := twofactor.URL(secret, v.session.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	var groups []string
	for rest := secret; rest != ""; {
		n := min(4, len(rest))
		groups = append(groups, rest[:n])
		rest = rest[n:]
	}
	s.show(w, v, status, twoFactorView, pageData{Title: "Two-factor", Error: problem,
		Data: twoFactorData{Secret: strings.Join(groups, " "), URL: template.URL(u)}}) //nolint:gosec // We made u.
}

// twoFactorSetup starts setting two-factor up with a new secret each time, so that a
// secret someone glimpsed on an abandoned page is no use.
func (s *site) twoFactorSetup(w http.ResponseWriter, r *http.Request, v visitor) {
	if v.session.HasTOTP || !s.canUseTwoFactor(v) {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	secret, err := twofactor.NewSecret(v.session.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	err = s.opts.Store.StartTOTP(r.Context(), v.actor.UserID, secret)
	if errors.Is(err, store.ErrConflict) {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.showTwoFactorSetup(w, v, http.StatusOK, secret, "")
}

// twoFactorQR shows the secret being set up. Once it's on, it's never shown again.
func (s *site) twoFactorQR(w http.ResponseWriter, r *http.Request, v visitor) {
	totp, err := s.opts.Store.GetTOTP(r.Context(), v.actor.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if totp.Enabled || totp.Secret == "" {
		http.NotFound(w, r)
		return
	}
	img, err := twofactor.QR(totp.Secret, v.session.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(img)
}

func (s *site) enableTwoFactor(w http.ResponseWriter, r *http.Request, v visitor) {
	ctx := r.Context()
	totp, err := s.opts.Store.GetTOTP(ctx, v.actor.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if totp.Enabled || totp.Secret == "" {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	step, ok := twofactor.Check(totp.Secret, r.PostForm.Get("code"), s.opts.Now())
	if !ok {
		s.showTwoFactorSetup(w, v, http.StatusUnprocessableEntity, totp.Secret, wrongCode+" Check your phone's clock, and try the next one.")
		return
	}
	codes, err := twofactor.RecoveryCodes()
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.opts.Store.EnableTOTP(ctx, v.actor.UserID, step, codes, s.opts.Now()); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(ctx, v.session.Name, "two-factor.on", "", s.client(r))
	s.show(w, v, http.StatusOK, recoveryView, pageData{Title: "Recovery codes", Data: codes})
}

func (s *site) disableTwoFactor(w http.ResponseWriter, r *http.Request, v visitor) {
	ctx := r.Context()
	ok, err := s.checkCode(ctx, v.actor.UserID, r.PostForm.Get("code"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.codes.WithLabelValues("wrong").Inc()
		s.audit(ctx, v.session.Name, "two-factor.off.failed", "", s.client(r))
		s.showAccount(w, r, v, http.StatusUnauthorized, wrongCode, "", "")
		return
	}
	if err := s.opts.Store.DisableTOTP(ctx, v.actor.UserID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(ctx, v.session.Name, "two-factor.off", "", s.client(r))
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// canUseTwoFactor is for password logins. A provider does its own.
func (s *site) canUseTwoFactor(v visitor) bool {
	return s.opts.PasswordLogin && v.session.HasPassword
}

func (s *site) requireTwoFactor(w http.ResponseWriter, r *http.Request, v visitor) {
	on := r.PostForm.Get("require") == "1"
	if err := s.opts.Store.SetRequireTOTP(r.Context(), on); err != nil {
		s.fail(w, err)
		return
	}
	what := "two-factor.optional"
	if on {
		what = "two-factor.required"
	}
	s.audit(r.Context(), v.session.Name, "settings.changed", what, s.client(r))
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// resetTwoFactor is for someone who's lost their phone and their recovery codes.
func (s *site) resetTwoFactor(w http.ResponseWriter, r *http.Request, v visitor) {
	user, ok := s.userOf(w, r)
	if !ok {
		return
	}
	if err := s.opts.Store.DisableTOTP(r.Context(), user.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r.Context(), v.session.Name, "two-factor.off", user.Name, s.client(r))
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
