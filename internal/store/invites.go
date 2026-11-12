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

const (
	// InviteLife is how long an invite link works unless it says otherwise, and
	// MaxInviteLife is the longest it can.
	InviteLife    = 7 * 24 * time.Hour
	MaxInviteLife = 30 * 24 * time.Hour
	// MaxInviteUses is the most people one link can let in.
	MaxInviteUses = 100
)

// Who can make invites.
const (
	InvitesByAdmins = "admins"
	InvitesByUsers  = "users"
)

var (
	// ErrNotAllowed means the actor can't do this at all.
	ErrNotAllowed = errors.New("not allowed")
	// ErrInviteLimit means a user's invites would let in more people than the limit.
	ErrInviteLimit = errors.New("that's more invite places than you have left")
	// ErrBadInvite means an invite asked for something it can't have.
	ErrBadInvite = errors.New("an invite is for 1 to 100 people, for up to 30 days, and a name or a reset is for one person")
)

// InviteRequest is what an invite is for.
type InviteRequest struct {
	// Name is who it's for. Empty lets whoever uses it pick their own.
	Name string
	Role string
	// Reset makes it for an existing user, to set a new password.
	Reset bool
	// MaxUses is how many people can use it. Zero means one.
	MaxUses int32
	// Expires is when it stops working. Zero means InviteLife from now.
	Expires time.Time
}

// Invite is an invite link, without the link.
type Invite struct {
	ID int64
	// Name is who it's for, or empty if whoever uses it picks.
	Name                 string
	Role                 string
	Reset                bool
	MaxUses, Uses        int32
	CreatedAt, ExpiresAt time.Time
	// CreatedBy is the name of whoever made it, or empty if the server did.
	CreatedBy string
}

func inviteOf(i db.Invite, uses int32, creator *string) Invite {
	return Invite{
		ID: i.ID, Name: deref(i.Name), Role: i.Role, Reset: i.UserID != nil, MaxUses: i.MaxUses, Uses: uses,
		CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt, CreatedBy: deref(creator),
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// InviteSettings says who can invite people.
type InviteSettings struct {
	// Who is InvitesByAdmins or InvitesByUsers.
	Who string
	// Limit is how many unused places a user, not an admin, can have out at once.
	Limit int32
}

// GetInviteSettings returns who can invite people.
func (s *Store) GetInviteSettings(ctx context.Context) (InviteSettings, error) {
	row, err := s.queries.GetInviteSettings(ctx)
	if err != nil {
		return InviteSettings{}, fmt.Errorf("read settings: %w", err)
	}
	return InviteSettings{Who: row.WhoCanInvite, Limit: row.InviteLimit}, nil
}

// SetInviteSettings changes who can invite people. A nil leaves that setting as it is.
func (s *Store) SetInviteSettings(ctx context.Context, who *string, limit *int32) error {
	if who != nil && *who != InvitesByAdmins && *who != InvitesByUsers {
		return fmt.Errorf("who can invite is %q: %w", *who, ErrBadInvite)
	}
	if limit != nil && *limit < 0 {
		return fmt.Errorf("invite limit %d: %w", *limit, ErrBadInvite)
	}
	if err := s.queries.SetInviteSettings(ctx, db.SetInviteSettingsParams{WhoCanInvite: who, InviteLimit: limit}); err != nil {
		return fmt.Errorf("change settings: %w", err)
	}
	return nil
}

// CreateInvite makes an invite link. An admin can invite anyone or make a reset link.
// A user can only invite users, when the site lets them, and only up to the invite
// limit; otherwise it's ErrNotAllowed or ErrInviteLimit. A named invite's name must be
// free, and a reset's must exist (ErrConflict, ErrNotFound). The actor is Holder for
// the server itself. It returns the raw token, which exists nowhere else.
func (s *Store) CreateInvite(ctx context.Context, by Actor, req InviteRequest, now time.Time) (Invite, string, error) {
	if req.MaxUses == 0 {
		req.MaxUses = 1
	}
	if req.Expires.IsZero() {
		req.Expires = now.Add(InviteLife)
	}
	switch {
	case req.MaxUses < 1 || req.MaxUses > MaxInviteUses, !req.Expires.After(now), req.Expires.After(now.Add(MaxInviteLife)),
		req.Name != "" && req.MaxUses != 1, req.Reset && req.Name == "":
		return Invite{}, "", ErrBadInvite
	case req.Name != "" && !validName.MatchString(req.Name):
		return Invite{}, "", ErrBadName
	case !by.Admin && (req.Role != RoleUser || req.Reset):
		return Invite{}, "", ErrNotAllowed
	}
	var (
		invite db.Invite
		raw    string
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var createdBy *int64
		if by.UserID != 0 {
			createdBy = &by.UserID
		}
		if !by.Admin {
			if err := allowedToInvite(ctx, q, by.UserID, req.MaxUses, now); err != nil {
				return err
			}
		}
		var userID *int64
		var name *string
		if req.Name != "" {
			name = &req.Name
			user, err := q.GetUserByName(ctx, req.Name)
			switch {
			case err == nil && !req.Reset:
				return fmt.Errorf("invite %s: %w", req.Name, ErrConflict)
			case err == nil:
				userID, req.Role = &user.ID, user.Role
			case !errors.Is(err, pgx.ErrNoRows) || req.Reset:
				return err
			}
		}
		var err error
		if raw, err = newToken(); err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(raw))
		invite, err = q.InsertInvite(ctx, db.InsertInviteParams{
			TokenHash: hash[:], Name: name, Role: req.Role, UserID: userID, CreatedAt: now, ExpiresAt: req.Expires,
			MaxUses: req.MaxUses, CreatedBy: createdBy,
		})
		return err
	})
	if err != nil {
		return Invite{}, "", fmt.Errorf("create invite: %w", translate(err))
	}
	return inviteOf(invite, 0, nilIfEmpty(by.Name)), raw, nil
}

// allowedToInvite checks that a user can make an invite for uses more people. Holding
// their row means two invites made at once can't both squeeze under the limit.
func allowedToInvite(ctx context.Context, q *db.Queries, userID int64, uses int32, now time.Time) error {
	user, err := q.GetUserForUpdate(ctx, userID)
	if err != nil {
		return err
	}
	settings, err := q.GetInviteSettings(ctx)
	if err != nil {
		return err
	}
	if user.DisabledAt != nil || settings.WhoCanInvite != InvitesByUsers {
		return ErrNotAllowed
	}
	out, err := q.CountInvitePlaces(ctx, db.CountInvitePlacesParams{CreatedBy: &userID, ExpiresAt: now})
	if err != nil {
		return err
	}
	if out+uses > settings.InviteLimit {
		return ErrInviteLimit
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// usable says whether an invite still works: it has uses left, and whoever made it
// could still make it.
func usable(row db.GetInviteRow) bool {
	switch {
	case row.Uses >= row.Invite.MaxUses:
		return false
	case row.Invite.CreatedBy == nil:
		return true
	case row.CreatorDisabled:
		return false
	case deref(row.CreatorRole) == RoleAdmin:
		return true
	}
	return row.WhoCanInvite == InvitesByUsers && row.Invite.Role == RoleUser && row.Invite.UserID == nil
}

// LookupInvite returns the working invite raw stands for, or ErrNotFound.
func (s *Store) LookupInvite(ctx context.Context, raw string, now time.Time) (Invite, error) {
	hash := sha256.Sum256([]byte(raw))
	row, err := s.queries.GetInvite(ctx, db.GetInviteParams{TokenHash: hash[:], Now: now})
	if err == nil && !usable(row) {
		err = pgx.ErrNoRows
	}
	if err != nil {
		return Invite{}, fmt.Errorf("look up invite: %w", translate(err))
	}
	return inviteOf(row.Invite, row.Uses, row.CreatorName), nil
}

// ListInvites returns up to limit invites with uses left and IDs above after: the
// actor's own, or everyone's for an admin.
func (s *Store) ListInvites(ctx context.Context, by Actor, after int64, limit int32, now time.Time) ([]Invite, error) {
	rows, err := s.queries.ListInvites(ctx, db.ListInvitesParams{
		After: after, Now: now, Everyone: by.Admin, UserID: &by.UserID, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	out := make([]Invite, 0, len(rows))
	for _, row := range rows {
		out = append(out, inviteOf(row.Invite, row.Uses, row.CreatorName))
	}
	return out, nil
}

// InvitePlaces is how many more people the actor's own live invites could let in,
// which is what counts against the invite limit.
func (s *Store) InvitePlaces(ctx context.Context, by Actor, now time.Time) (int32, error) {
	out, err := s.queries.CountInvitePlaces(ctx, db.CountInvitePlacesParams{CreatedBy: &by.UserID, ExpiresAt: now})
	if err != nil {
		return 0, fmt.Errorf("count invite places: %w", err)
	}
	return out, nil
}

// DeleteInvite withdraws an invite: the actor's own, or anyone's for an admin.
// Someone else's is ErrNotFound.
func (s *Store) DeleteInvite(ctx context.Context, by Actor, id int64) error {
	n, err := s.queries.DeleteInvite(ctx, db.DeleteInviteParams{ID: id, Everyone: by.Admin, UserID: &by.UserID})
	if err != nil {
		return fmt.Errorf("delete invite %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("delete invite %d: %w", id, ErrNotFound)
	}
	return nil
}

// RedeemInvite uses an invite: it sets the password, creating the user if the invite
// is for a new one, ends every other session, and logs them in. name is the name
// picked by whoever is using an invite that doesn't have one. Holding the invite's row
// means two people can't both take its last use.
func (s *Store) RedeemInvite(ctx context.Context, raw, name, passwordHash string, client Client, now time.Time) (db.User, string, error) {
	return s.redeem(ctx, raw, name, MethodPassword, client, now, func(q *db.Queries, user db.User) error {
		return q.SetPassword(ctx, db.SetPasswordParams{ID: user.ID, PasswordHash: &passwordHash})
	})
}

// RedeemInviteWithIdentity is RedeemInvite for someone who logged in at an OpenID
// provider: the identity is linked to the user in place of a password. It's
// ErrConflict if the identity already belongs to someone.
func (s *Store) RedeemInviteWithIdentity(ctx context.Context, raw, name string, id Identity, client Client, now time.Time) (db.User, string, error) {
	return s.redeem(ctx, raw, name, MethodOIDC, client, now, func(q *db.Queries, user db.User) error {
		return q.InsertIdentity(ctx, db.InsertIdentityParams{UserID: user.ID, Issuer: id.Issuer, Subject: id.Subject, CreatedAt: now})
	})
}

func (s *Store) redeem(ctx context.Context, raw, name, method string, client Client, now time.Time, set func(*db.Queries, db.User) error) (db.User, string, error) {
	hash := sha256.Sum256([]byte(raw))
	var (
		user    db.User
		session string
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		// The users lock comes first, as everywhere else, so that a bootstrap invite and
		// anything else making an admin can't cross.
		if err := q.LockUsers(ctx, usersLockID); err != nil {
			return err
		}
		// The uses are counted after the row is held, so that the count includes
		// anyone who got there first.
		if err := q.LockInvite(ctx, hash[:]); err != nil {
			return err
		}
		row, err := q.GetInvite(ctx, db.GetInviteParams{TokenHash: hash[:], Now: now})
		if err != nil {
			return err
		}
		if !usable(row) {
			return pgx.ErrNoRows
		}
		invite := row.Invite
		if invite.Bootstrap {
			// A bootstrap invite is dead once someone else has become an admin.
			admins, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if admins > 0 {
				return pgx.ErrNoRows
			}
			if err := q.DeleteBootstrapToken(ctx); err != nil {
				return err
			}
		}
		switch {
		case invite.UserID != nil:
			user, err = q.GetUser(ctx, *invite.UserID)
		case invite.Name != nil:
			user, err = q.InsertInvitedUser(ctx, db.InsertInvitedUserParams{Name: *invite.Name, Role: invite.Role, InvitedBy: row.CreatorName})
		case !validName.MatchString(name):
			return ErrBadName
		default:
			user, err = q.InsertInvitedUser(ctx, db.InsertInvitedUserParams{Name: name, Role: invite.Role, InvitedBy: row.CreatorName})
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
		if err := q.UseInvite(ctx, db.UseInviteParams{InviteID: invite.ID, UserID: &user.ID, UsedAt: now}); err != nil {
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
