-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- Who can make invites, admins or everyone. A user can have at most invite_limit unused
-- places out at once, counting each use left on each of their links.
ALTER TABLE settings
    ADD COLUMN who_can_invite text NOT NULL DEFAULT 'admins' CHECK (who_can_invite IN ('admins', 'users')),
    ADD COLUMN invite_limit integer NOT NULL DEFAULT 5 CHECK (invite_limit >= 0);

-- Whose invite made the account. It's a name and not a key, like the audit log, so that
-- it outlives the inviter.
ALTER TABLE users ADD COLUMN invited_by text;

-- An invite with no name lets whoever uses it pick their own. created_by is null for one
-- the server made itself, with the bootstrap token or from its command line.
ALTER TABLE invites
    ALTER COLUMN name DROP NOT NULL,
    ADD COLUMN max_uses integer NOT NULL DEFAULT 1 CHECK (max_uses >= 1),
    ADD COLUMN created_by bigint REFERENCES users ON DELETE CASCADE;
CREATE INDEX invites_created_by ON invites (created_by);

-- Each use of an invite, and who it made or reset.
CREATE TABLE invite_uses (
    invite_id bigint NOT NULL REFERENCES invites ON DELETE CASCADE,
    user_id   bigint REFERENCES users ON DELETE SET NULL,
    used_at   timestamptz NOT NULL
);
CREATE INDEX invite_uses_invite ON invite_uses (invite_id);

INSERT INTO invite_uses (invite_id, user_id, used_at)
SELECT i.id, coalesce(i.user_id, u.id), i.used_at
FROM invites i LEFT JOIN users u ON i.user_id IS NULL AND u.name = i.name
WHERE i.used_at IS NOT NULL;

ALTER TABLE invites DROP COLUMN used_at;

-- +goose Down
ALTER TABLE invites ADD COLUMN used_at timestamptz;
UPDATE invites SET used_at = (SELECT max(used_at) FROM invite_uses WHERE invite_id = invites.id);
DROP TABLE invite_uses;
DELETE FROM invites WHERE name IS NULL;
ALTER TABLE invites DROP COLUMN created_by, DROP COLUMN max_uses, ALTER COLUMN name SET NOT NULL;
ALTER TABLE users DROP COLUMN invited_by;
ALTER TABLE settings DROP COLUMN invite_limit, DROP COLUMN who_can_invite;
