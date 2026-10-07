-- +goose Up
-- Readers report posts, comments and users; moderators resolve or dismiss.
CREATE TABLE reports (
    id          SERIAL PRIMARY KEY,
    reporter_id INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_type TEXT NOT NULL CHECK (target_type IN ('post', 'comment', 'user')),
    target_id   INT NOT NULL,
    reason      TEXT NOT NULL CHECK (reason IN ('spam', 'abuse', 'harassment', 'off_topic', 'other')),
    note        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by INT REFERENCES users (id) ON DELETE SET NULL,
    resolved_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One open report per reporter and target.
CREATE UNIQUE INDEX reports_open_unique_idx ON reports (reporter_id, target_type, target_id) WHERE status = 'open';
CREATE INDEX reports_status_idx ON reports (status, id DESC);
CREATE INDEX reports_target_idx ON reports (target_type, target_id);

-- +goose Down
DROP TABLE reports;
