-- +goose Up
-- Post revisions: a snapshot of the previous title, subtitle and body is
-- kept on every edit that changes them.
CREATE TABLE post_revisions (
    id         SERIAL PRIMARY KEY,
    post_id    INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    version    INT NOT NULL,
    title      TEXT NOT NULL,
    subtitle   TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL,
    editor_id  INT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (post_id, version)
);

-- Series: an author's posts read in order. A post is in at most one series.
CREATE TABLE series (
    id          SERIAL PRIMARY KEY,
    user_id     INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slug        TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX series_slug_idx ON series (slug);
CREATE INDEX series_user_id_idx ON series (user_id);

CREATE TABLE series_posts (
    series_id INT NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    post_id   INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    position  INT NOT NULL,
    PRIMARY KEY (series_id, post_id),
    UNIQUE (post_id)
);

-- +goose Down
DROP TABLE series_posts;
DROP TABLE series;
DROP TABLE post_revisions;
