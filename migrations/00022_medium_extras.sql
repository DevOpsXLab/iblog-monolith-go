-- +goose Up
-- Unlisted posts: readable by link, left out of every listing.
ALTER TABLE posts DROP CONSTRAINT posts_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_status_check CHECK (status IN ('draft', 'scheduled', 'published', 'unlisted'));

-- Where an imported story first appeared.
ALTER TABLE posts ADD COLUMN canonical_url TEXT NOT NULL DEFAULT '';

-- A profile's pinned story.
ALTER TABLE users ADD COLUMN pinned_post_id INT REFERENCES posts (id) ON DELETE SET NULL;

-- Named reading lists.
CREATE TABLE lists (
    id          SERIAL PRIMARY KEY,
    user_id     INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    private     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX lists_slug_idx ON lists (slug);
CREATE INDEX lists_user_id_idx ON lists (user_id);

CREATE TABLE list_items (
    list_id  INT NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    post_id  INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, post_id)
);
CREATE INDEX list_items_post_id_idx ON list_items (post_id);

-- Reads: a reader who got through most of a post. reader is "user:<id>" or
-- "ip:<addr>", so each counts once.
CREATE TABLE post_reads (
    post_id INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    reader  TEXT NOT NULL,
    user_id INT REFERENCES users (id) ON DELETE SET NULL,
    read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, reader)
);
CREATE INDEX post_reads_user_id_idx ON post_reads (user_id) WHERE user_id IS NOT NULL;

-- "Show less like this": a post, an author (user id) or a tag kept out of
-- the reader's feeds.
CREATE TABLE feed_hides (
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('post', 'author', 'tag')),
    target     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind, target)
);

-- Email when an author publishes.
CREATE TABLE email_subscriptions (
    subscriber_id INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    author_id     INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber_id, author_id),
    CHECK (subscriber_id <> author_id)
);
CREATE INDEX email_subscriptions_author_id_idx ON email_subscriptions (author_id);

-- +goose Down
DROP TABLE email_subscriptions;
DROP TABLE feed_hides;
DROP TABLE post_reads;
DROP TABLE list_items;
DROP TABLE lists;
ALTER TABLE users DROP COLUMN pinned_post_id;
ALTER TABLE posts DROP COLUMN canonical_url;
UPDATE posts SET status = 'published' WHERE status = 'unlisted';
ALTER TABLE posts DROP CONSTRAINT posts_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_status_check CHECK (status IN ('draft', 'scheduled', 'published'));
