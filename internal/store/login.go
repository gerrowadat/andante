// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// How long a session lasts. All times here come from the caller, so that a test can
// move the clock.
const (
	SessionIdle     = 7 * 24 * time.Hour
	SessionAbsolute = 30 * 24 * time.Hour
	// InviteLife is how long an invite link works.
	InviteLife = 7 * 24 * time.Hour
)

// Session is a logged-in browser.
type Session struct {
	ID     int64
	UserID int64
	Name   string
	Role   string
	Method string
	// HasPassword and HasTOTP say what the user has set up.
	HasPassword, HasTOTP bool
	// NeedsTOTP means two-factor is required and this session has to set it up
	// before it can do anything else.
	NeedsTOTP bool
}

// Client is where a request came from.
type Client struct {
	IP        string
	UserAgent string
}

// GetUserByName returns a user, or ErrNotFound.
func (s *Store) GetUserByName(ctx context.Context, name string) (db.User, error) {
	user, err := s.queries.GetUserByName(ctx, name)
	if err != nil {
		return db.User{}, fmt.Errorf("get user: %w", translate(err))
	}
	return user, nil
}

func insertSession(ctx context.Context, q *db.Queries, userID int64, method string, client Client, now time.Time) (string, error) {
	raw, err := newToken()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(raw))
	_, err = q.InsertSession(ctx, db.InsertSessionParams{
		UserID: userID, TokenHash: hash[:], Ip: client.IP, UserAgent: client.UserAgent, CreatedAt: now, Method: method,
	})
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return raw, nil
}

// CreateSession logs a user in with a password, and returns the cookie's value, which
// exists nowhere else.
func (s *Store) CreateSession(ctx context.Context, userID int64, client Client, now time.Time) (string, error) {
	return insertSession(ctx, s.queries, userID, MethodPassword, client, now)
}

// LookupSession returns the live session raw stands for, or ErrNotFound. It notes
// the session's use at most once a minute.
func (s *Store) LookupSession(ctx context.Context, raw string, now time.Time) (Session, error) {
	hash := sha256.Sum256([]byte(raw))
	row, err := s.queries.GetLiveSession(ctx, db.GetLiveSessionParams{
		TokenHash: hash[:], CreatedAfter: now.Add(-SessionAbsolute), SeenAfter: now.Add(-SessionIdle),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("look up session: %w", err)
	}
	if now.Sub(row.LastSeenAt) > time.Minute {
		if err := s.queries.TouchSession(ctx, db.TouchSessionParams{ID: row.ID, LastSeenAt: now}); err != nil {
			return Session{}, fmt.Errorf("record session use: %w", err)
		}
	}
	return Session{
		ID: row.ID, UserID: row.UserID, Name: row.Name, Role: row.Role, Method: row.Method,
		HasPassword: row.HasPassword, HasTOTP: row.HasTotp,
		NeedsTOTP: row.RequireTotp && row.Method == MethodPassword && !row.HasTotp,
	}, nil
}

// ListSessions returns up to limit of a user's live sessions with IDs above after.
func (s *Store) ListSessions(ctx context.Context, actor Actor, after int64, limit int32, now time.Time) ([]db.Session, error) {
	sessions, err := s.queries.ListLiveSessions(ctx, db.ListLiveSessionsParams{
		UserID: actor.UserID, After: after, CreatedAfter: now.Add(-SessionAbsolute), SeenAfter: now.Add(-SessionIdle), PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return sessions, nil
}

// RevokeSession ends one of a user's own sessions. Someone else's is ErrNotFound, and
// that goes for admins too.
func (s *Store) RevokeSession(ctx context.Context, actor Actor, id int64, now time.Time) error {
	rows, err := s.queries.RevokeSession(ctx, db.RevokeSessionParams{ID: id, UserID: actor.UserID, At: &now})
	if err != nil {
		return fmt.Errorf("revoke session %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("revoke session %d: %w", id, ErrNotFound)
	}
	return nil
}

// EndSession ends the session raw stands for, if there is one. Logging out and
// logging in both use it, the second so that an old cookie can't carry on.
func (s *Store) EndSession(ctx context.Context, raw string, now time.Time) error {
	hash := sha256.Sum256([]byte(raw))
	if err := s.queries.RevokeSessionByHash(ctx, db.RevokeSessionByHashParams{TokenHash: hash[:], At: &now}); err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	return nil
}

// HasLoggedInFrom reports whether a user has had a session from an address in the last
// while, which is what lets them past an account-wide throttle a stranger set off.
func (s *Store) HasLoggedInFrom(ctx context.Context, userID int64, ip string, now time.Time) (bool, error) {
	known, err := s.queries.HasLoggedInFrom(ctx, db.HasLoggedInFromParams{UserID: userID, Ip: ip, Since: now.Add(-SessionAbsolute)})
	if err != nil {
		return false, fmt.Errorf("check login history: %w", err)
	}
	return known, nil
}

// CreateInvite makes a single-use link. With reset it's for an existing user to set
// a new password, and name must exist. Without, it creates the user, and name must be
// free. It returns the raw token, which exists nowhere else.
func (s *Store) CreateInvite(ctx context.Context, name, role string, reset bool, now time.Time) (db.Invite, string, error) {
	var userID *int64
	user, err := s.GetUserByName(ctx, name)
	switch {
	case err == nil && !reset:
		return db.Invite{}, "", fmt.Errorf("invite %s: %w", name, ErrConflict)
	case err == nil:
		userID, role = &user.ID, user.Role
	case !errors.Is(err, ErrNotFound) || reset:
		return db.Invite{}, "", err
	}
	raw, err := newToken()
	if err != nil {
		return db.Invite{}, "", err
	}
	hash := sha256.Sum256([]byte(raw))
	invite, err := s.queries.InsertInvite(ctx, db.InsertInviteParams{
		TokenHash: hash[:], Name: name, Role: role, UserID: userID, CreatedAt: now, ExpiresAt: now.Add(InviteLife),
	})
	if err != nil {
		return db.Invite{}, "", fmt.Errorf("insert invite: %w", err)
	}
	return invite, raw, nil
}

// LookupInvite returns the unused invite raw stands for, or ErrNotFound.
func (s *Store) LookupInvite(ctx context.Context, raw string, now time.Time) (db.Invite, error) {
	hash := sha256.Sum256([]byte(raw))
	invite, err := s.queries.GetLiveInvite(ctx, db.GetLiveInviteParams{TokenHash: hash[:], ExpiresAt: now})
	if err != nil {
		return db.Invite{}, fmt.Errorf("look up invite: %w", translate(err))
	}
	return invite, nil
}

// RedeemInvite uses an invite up: it sets the password, creating the user if the
// invite is for a new one, ends every other session, and logs them in. Holding the
// invite's row means two tabs can't both use it.
func (s *Store) RedeemInvite(ctx context.Context, raw, passwordHash string, client Client, now time.Time) (db.User, string, error) {
	return s.redeem(ctx, raw, MethodPassword, client, now, func(q *db.Queries, user db.User) error {
		return q.SetPassword(ctx, db.SetPasswordParams{ID: user.ID, PasswordHash: &passwordHash})
	})
}

// RedeemInviteWithIdentity is RedeemInvite for someone who logged in at an OpenID
// provider: the identity is linked to the user in place of a password. It's
// ErrConflict if the identity already belongs to someone.
func (s *Store) RedeemInviteWithIdentity(ctx context.Context, raw string, id Identity, client Client, now time.Time) (db.User, string, error) {
	return s.redeem(ctx, raw, MethodOIDC, client, now, func(q *db.Queries, user db.User) error {
		return q.InsertIdentity(ctx, db.InsertIdentityParams{UserID: user.ID, Issuer: id.Issuer, Subject: id.Subject, CreatedAt: now})
	})
}

func (s *Store) redeem(ctx context.Context, raw, method string, client Client, now time.Time, set func(*db.Queries, db.User) error) (db.User, string, error) {
	hash := sha256.Sum256([]byte(raw))
	var (
		user    db.User
		session string
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		invite, err := q.GetLiveInviteForUpdate(ctx, db.GetLiveInviteForUpdateParams{TokenHash: hash[:], ExpiresAt: now})
		if err != nil {
			return err
		}
		if invite.UserID == nil {
			user, err = q.InsertUser(ctx, db.InsertUserParams{Name: invite.Name, Role: invite.Role})
		} else {
			user, err = q.GetUser(ctx, *invite.UserID)
		}
		if err != nil {
			return err
		}
		if err := set(q, user); err != nil {
			return err
		}
		if err := q.RevokeAllSessions(ctx, db.RevokeAllSessionsParams{UserID: user.ID, At: &now}); err != nil {
			return err
		}
		if err := q.UseInvite(ctx, db.UseInviteParams{ID: invite.ID, UsedAt: &now}); err != nil {
			return err
		}
		session, err = insertSession(ctx, q, user.ID, method, client, now)
		return err
	})
	if err != nil {
		return db.User{}, "", fmt.Errorf("redeem invite: %w", translate(err))
	}
	return user, session, nil
}

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	Who    string // A user's name, or what ran the command.
	Action string
	Target string
	IP     string
}

// Audit adds to the audit log.
func (s *Store) Audit(ctx context.Context, entry AuditEntry, now time.Time) error {
	err := s.queries.InsertAudit(ctx, db.InsertAuditParams{At: now, Who: entry.Who, Action: entry.Action, Target: entry.Target, Ip: entry.IP})
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// ListAudit returns up to limit lines with IDs above after, oldest first.
func (s *Store) ListAudit(ctx context.Context, after int64, limit int32) ([]db.AuditLog, error) {
	lines, err := s.queries.ListAudit(ctx, db.ListAuditParams{ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	return lines, nil
}

// ChangePassword sets a new password and ends every other session the user has, so
// that whoever might have known the old one is logged out.
func (s *Store) ChangePassword(ctx context.Context, userID, keepSession int64, hash string, now time.Time) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.SetPassword(ctx, db.SetPasswordParams{ID: userID, PasswordHash: &hash}); err != nil {
			return err
		}
		return q.RevokeOtherSessions(ctx, db.RevokeOtherSessionsParams{At: &now, UserID: userID, Keep: keepSession})
	})
	if err != nil {
		return fmt.Errorf("change password of user %d: %w", userID, err)
	}
	return nil
}
