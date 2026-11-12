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
	// CanInvite means they can make invites.
	CanInvite bool
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
		CanInvite: row.Role == RoleAdmin || row.WhoCanInvite == InvitesByUsers,
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

// CountSessions returns how many sessions are live, by how they logged in.
func (s *Store) CountSessions(ctx context.Context, now time.Time) (map[string]int64, error) {
	rows, err := s.queries.CountLiveSessions(ctx, db.CountLiveSessionsParams{
		CreatedAfter: now.Add(-SessionAbsolute), SeenAfter: now.Add(-SessionIdle),
	})
	if err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Method] = row.Sessions
	}
	return counts, nil
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
