-- +goose Up
-- Email verification.
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;

-- Comment likes; comments.likes is a denormalized counter.
ALTER TABLE comments ADD COLUMN likes INT NOT NULL DEFAULT 0;
CREATE TABLE comment_likes (
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    comment_id INT NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, comment_id)
);
CREATE INDEX comment_likes_comment_idx ON comment_likes (comment_id);

-- Followed tags feed the reader's feed.
CREATE TABLE tag_follows (
    user_id    INT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tag        TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, tag)
);

-- Keyset paging for comments, follows and notifications walks these.
CREATE INDEX comments_post_id_id_idx ON comments (post_id, id);
CREATE INDEX follows_follower_created_idx ON follows (follower_id, created_at DESC);
CREATE INDEX follows_followee_created_idx ON follows (followee_id, created_at DESC);

-- +goose Down
DROP INDEX follows_followee_created_idx;
DROP INDEX follows_follower_created_idx;
DROP INDEX comments_post_id_id_idx;
DROP TABLE tag_follows;
DROP TABLE comment_likes;
ALTER TABLE comments DROP COLUMN likes;
ALTER TABLE users DROP COLUMN email_verified_at;
