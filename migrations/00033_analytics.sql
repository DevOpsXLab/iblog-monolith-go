-- +goose Up
-- Product analytics: page views, read time and the signup funnel.
-- visitor is a random id the client keeps only after cookie consent; no IP
-- or user agent is stored. Rows older than the retention window are purged
-- by the worker.
CREATE TABLE analytics_events (
    id         BIGSERIAL PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    name       TEXT NOT NULL,
    visitor    TEXT NOT NULL,
    user_id    INT REFERENCES users(id) ON DELETE SET NULL,
    path       TEXT NOT NULL DEFAULT '',
    post_id    INT REFERENCES posts(id) ON DELETE SET NULL,
    value      INT NOT NULL DEFAULT 0,
    lang       TEXT NOT NULL DEFAULT '',
    referrer   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX analytics_events_at_idx ON analytics_events (at);
CREATE INDEX analytics_events_name_at_idx ON analytics_events (name, at);
CREATE INDEX analytics_events_user_idx ON analytics_events (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX analytics_events_post_idx ON analytics_events (post_id) WHERE post_id IS NOT NULL;

-- +goose Down
DROP TABLE analytics_events;
