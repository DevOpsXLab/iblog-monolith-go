-- +goose NO TRANSACTION
-- +goose Up
-- Post search matches p.title ILIKE '%q%'; a leading wildcard cannot use a
-- btree, so without this every search seq-scans posts. pg_trgm GIN serves
-- ILIKE '%q%' for q of 3+ characters.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX CONCURRENTLY IF NOT EXISTS posts_title_trgm_idx ON posts USING GIN (title gin_trgm_ops);

-- +goose Down
-- The extension stays: other objects may depend on it.
DROP INDEX CONCURRENTLY IF EXISTS posts_title_trgm_idx;
