-- +goose Up
-- Claps: a like row carries 1-50 claps; posts.likes stays the number of
-- clappers and posts.claps the total.
ALTER TABLE likes ADD COLUMN claps INT NOT NULL DEFAULT 1 CHECK (claps BETWEEN 1 AND 50);
ALTER TABLE posts ADD COLUMN claps INT NOT NULL DEFAULT 0;
UPDATE posts SET claps = likes;

-- Highlights: a reader marks a passage of a post's body.
CREATE TABLE highlights (
    id           SERIAL PRIMARY KEY,
    user_id      INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    post_id      INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    text         TEXT NOT NULL,
    start_offset INT NOT NULL CHECK (start_offset >= 0),
    end_offset   INT NOT NULL CHECK (end_offset > start_offset),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX highlights_post_id_idx ON highlights (post_id);
CREATE INDEX highlights_user_id_idx ON highlights (user_id);

-- +goose Down
DROP TABLE highlights;
ALTER TABLE posts DROP COLUMN claps;
ALTER TABLE likes DROP COLUMN claps;
