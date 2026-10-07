-- +goose Up
-- Keyset pagination walks posts by (sort time, id), the same key List orders by.
CREATE INDEX posts_sort_key_idx ON posts ((COALESCE(published_at, publish_at, updated_at)) DESC, id DESC);

-- +goose Down
DROP INDEX posts_sort_key_idx;
