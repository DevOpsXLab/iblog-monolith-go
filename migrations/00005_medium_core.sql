-- +goose Up
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN bio          TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar_url   TEXT NOT NULL DEFAULT '';

ALTER TABLE posts ADD COLUMN subtitle     TEXT NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN slug         TEXT;
ALTER TABLE posts ADD COLUMN status       TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('draft', 'published'));
ALTER TABLE posts ADD COLUMN published_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN reading_time INT NOT NULL DEFAULT 1;

UPDATE posts SET
    slug = 'post-' || id,
    published_at = created_at,
    reading_time = GREATEST(1, CEIL(array_length(regexp_split_to_array(trim(body), '\s+'), 1) / 200.0)::INT);
ALTER TABLE posts ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX posts_slug_idx ON posts (slug);
CREATE INDEX posts_published_idx ON posts (published_at DESC, id DESC) WHERE status = 'published';

CREATE TABLE follows (
    follower_id INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    followee_id INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX follows_followee_id_idx ON follows (followee_id);

-- +goose Down
DROP TABLE follows;
DROP INDEX posts_published_idx;
DROP INDEX posts_slug_idx;
ALTER TABLE posts DROP COLUMN reading_time;
ALTER TABLE posts DROP COLUMN published_at;
ALTER TABLE posts DROP COLUMN status;
ALTER TABLE posts DROP COLUMN slug;
ALTER TABLE posts DROP COLUMN subtitle;
ALTER TABLE users DROP COLUMN avatar_url;
ALTER TABLE users DROP COLUMN bio;
ALTER TABLE users DROP COLUMN display_name;
