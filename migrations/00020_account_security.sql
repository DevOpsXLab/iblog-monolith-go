-- +goose Up
-- Two-factor authentication (TOTP). secret is encrypted by the app;
-- enabled_at is NULL while setup is pending. last_step blocks code replay.
CREATE TABLE user_mfa (
    user_id    INT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret     TEXT NOT NULL,
    enabled_at TIMESTAMPTZ,
    last_step  BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Single-use recovery codes, stored as SHA-256 hashes.
CREATE TABLE user_backup_codes (
    user_id   INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at   TIMESTAMPTZ,
    PRIMARY KEY (user_id, code_hash)
);

-- Bans (until IS NULL) and suspensions (until set). Guard's account status
-- enforces them; this table keeps the reason and the end date.
CREATE TABLE user_sanctions (
    user_id    INT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    reason     TEXT NOT NULL,
    until      TIMESTAMPTZ,
    actor_id   INT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX user_sanctions_until_idx ON user_sanctions (until) WHERE until IS NOT NULL;

-- Blocks and mutes between users. kind 'block' stops follows, comments and
-- replies; 'mute' only silences notifications.
CREATE TABLE user_relations (
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_id  INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('block', 'mute')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind, target_id),
    CHECK (user_id <> target_id)
);
CREATE INDEX user_relations_target_idx ON user_relations (target_id, kind);

-- +goose Down
DROP TABLE user_relations;
DROP TABLE user_sanctions;
DROP TABLE user_backup_codes;
DROP TABLE user_mfa;
