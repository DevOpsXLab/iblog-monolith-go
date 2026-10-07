-- +goose Up
-- Publications: team blogs. Owners manage editors and writers; editors
-- manage writers and edit any post in the publication.
CREATE TABLE publications (
    id          SERIAL PRIMARY KEY,
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    avatar_url  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX publications_slug_idx ON publications (slug);

CREATE TABLE publication_members (
    publication_id INT NOT NULL REFERENCES publications (id) ON DELETE CASCADE,
    user_id        INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role           TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'writer')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (publication_id, user_id)
);
CREATE INDEX publication_members_user_idx ON publication_members (user_id);

ALTER TABLE posts ADD COLUMN publication_id INT REFERENCES publications (id) ON DELETE SET NULL;
CREATE INDEX posts_publication_id_idx ON posts (publication_id);

-- +goose Down
ALTER TABLE posts DROP COLUMN publication_id;
DROP TABLE publication_members;
DROP TABLE publications;
