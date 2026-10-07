-- +goose Up
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'));

-- One like per user per post; posts.likes stays as a denormalized counter.
CREATE TABLE likes (
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    post_id    INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, post_id)
);
CREATE INDEX likes_post_id_idx ON likes (post_id);

ALTER TABLE comments ADD COLUMN user_id INT REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE posts ADD COLUMN cover_url TEXT NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN search tsvector
    GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || body)) STORED;
CREATE INDEX posts_search_idx ON posts USING GIN (search);

-- +goose Down
DROP INDEX posts_search_idx;
ALTER TABLE posts DROP COLUMN search;
ALTER TABLE posts DROP COLUMN cover_url;
ALTER TABLE comments DROP COLUMN user_id;
DROP TABLE likes;
ALTER TABLE users DROP COLUMN role;
