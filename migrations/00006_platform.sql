-- +goose Up
-- Credentials, sessions, roles and policies move to Guard (guard_* tables).
ALTER TABLE users DROP COLUMN password_hash;
ALTER TABLE users DROP COLUMN role;
ALTER TABLE users ADD COLUMN email TEXT;
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ; -- deletion requested; purged 30 days later
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

-- Scheduled publishing.
ALTER TABLE posts DROP CONSTRAINT posts_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_status_check CHECK (status IN ('draft', 'scheduled', 'published'));
ALTER TABLE posts ADD COLUMN publish_at TIMESTAMPTZ;
CREATE INDEX posts_scheduled_idx ON posts (publish_at) WHERE status = 'scheduled';

-- Comment replies and edits.
ALTER TABLE comments ADD COLUMN parent_id INT REFERENCES comments (id) ON DELETE CASCADE;
ALTER TABLE comments ADD COLUMN edited_at TIMESTAMPTZ;
CREATE INDEX comments_parent_id_idx ON comments (parent_id);
CREATE INDEX comments_user_id_idx ON comments (user_id);

-- Labels: curated and coloured (tags stay free-form).
CREATE TABLE labels (
    id    SERIAL PRIMARY KEY,
    name  TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#6b7280'
);
CREATE UNIQUE INDEX labels_name_lower_idx ON labels (lower(name));
CREATE TABLE post_labels (
    post_id  INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    label_id INT NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, label_id)
);
CREATE INDEX post_labels_label_id_idx ON post_labels (label_id);

CREATE TABLE bookmarks (
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    post_id    INT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, post_id)
);

CREATE TABLE notifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    actor_id   INT REFERENCES users (id) ON DELETE CASCADE,
    post_id    INT REFERENCES posts (id) ON DELETE CASCADE,
    comment_id INT REFERENCES comments (id) ON DELETE CASCADE,
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON notifications (user_id, id DESC);

-- +goose Down
DROP TABLE notifications;
DROP TABLE bookmarks;
DROP TABLE post_labels;
DROP TABLE labels;
DROP INDEX comments_user_id_idx;
DROP INDEX comments_parent_id_idx;
ALTER TABLE comments DROP COLUMN edited_at;
ALTER TABLE comments DROP COLUMN parent_id;
DROP INDEX posts_scheduled_idx;
ALTER TABLE posts DROP COLUMN publish_at;
UPDATE posts SET status = 'draft' WHERE status = 'scheduled';
ALTER TABLE posts DROP CONSTRAINT posts_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_status_check CHECK (status IN ('draft', 'published'));
DROP INDEX users_email_lower_idx;
ALTER TABLE users DROP COLUMN deleted_at;
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user';
ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
