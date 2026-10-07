-- +goose NO TRANSACTION
-- +goose Up
-- Indexes on foreign-key columns that had none. Without them every
-- DELETE/UPDATE of the referenced row (posts, comments, users) seq-scans
-- the referencing table for ON DELETE CASCADE / SET NULL, and lookups by
-- these columns scan too. CONCURRENTLY: no write lock on live tables.
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_post_id_idx ON notifications (post_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_comment_id_idx ON notifications (comment_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS notifications_actor_id_idx ON notifications (actor_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS users_pinned_post_id_idx ON users (pinned_post_id) WHERE pinned_post_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS bookmarks_post_id_idx ON bookmarks (post_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS reports_resolved_by_idx ON reports (resolved_by) WHERE resolved_by IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS user_sanctions_actor_id_idx ON user_sanctions (actor_id) WHERE actor_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS post_revisions_editor_id_idx ON post_revisions (editor_id) WHERE editor_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS post_revisions_editor_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS user_sanctions_actor_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS reports_resolved_by_idx;
DROP INDEX CONCURRENTLY IF EXISTS bookmarks_post_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS users_pinned_post_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS notifications_actor_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS notifications_comment_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS notifications_post_id_idx;
