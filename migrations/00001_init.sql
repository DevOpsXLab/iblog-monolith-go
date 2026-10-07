-- +goose Up
CREATE TABLE categories (
    id   SERIAL PRIMARY KEY,
    name TEXT NOT NULL
);
CREATE UNIQUE INDEX categories_name_lower_idx ON categories (lower(name));

CREATE TABLE posts (
    id          SERIAL PRIMARY KEY,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    author      TEXT NOT NULL DEFAULT 'Anonymous',
    category_id INT REFERENCES categories (id) ON DELETE SET NULL,
    tags        TEXT[] NOT NULL DEFAULT '{}',
    likes       INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX posts_category_id_idx ON posts (category_id);
CREATE INDEX posts_tags_idx ON posts USING GIN (tags);

CREATE TABLE comments (
    id         SERIAL PRIMARY KEY,
    post_id    INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    author     TEXT NOT NULL DEFAULT 'Anonymous',
    text       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX comments_post_id_idx ON comments (post_id);

-- +goose Down
DROP TABLE comments;
DROP TABLE posts;
DROP TABLE categories;
