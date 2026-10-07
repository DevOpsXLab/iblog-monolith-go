-- +goose Up
ALTER TABLE posts ADD COLUMN user_id INT REFERENCES users (id) ON DELETE SET NULL;
CREATE INDEX posts_user_id_idx ON posts (user_id);

-- +goose Down
ALTER TABLE posts DROP COLUMN user_id;
