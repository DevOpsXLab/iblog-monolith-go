-- +goose NO TRANSACTION
-- +goose Up
-- One new_post notification per (recipient, post, author): a retried
-- fanout job used to insert duplicates. SocialRepo.Notify inserts with
-- ON CONFLICT (user_id, type, post_id, actor_id) WHERE type = 'new_post'
-- DO NOTHING, which needs this index.
--
-- 1. Drop existing duplicates, keeping the oldest row (min id). A copy that
--    was read marks the kept row read, so nothing reappears as unread.
UPDATE notifications k SET read_at = d.read_at
FROM (SELECT min(id) AS keep_id, min(read_at) AS read_at
      FROM notifications WHERE type = 'new_post'
      GROUP BY user_id, post_id, actor_id HAVING count(*) > 1) d
WHERE k.id = d.keep_id AND k.read_at IS NULL AND d.read_at IS NOT NULL;
DELETE FROM notifications n USING notifications k
WHERE n.type = 'new_post' AND k.type = 'new_post'
  AND n.user_id = k.user_id AND n.post_id = k.post_id AND n.actor_id = k.actor_id
  AND n.id > k.id;
-- 2. A failed concurrent build (a duplicate slipped in between 1 and 2)
--    leaves an INVALID index; drop it so a rerun rebuilds it.
DROP INDEX CONCURRENTLY IF EXISTS notifications_new_post_dedupe_idx;
CREATE UNIQUE INDEX CONCURRENTLY notifications_new_post_dedupe_idx
    ON notifications (user_id, type, post_id, actor_id) WHERE type = 'new_post';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS notifications_new_post_dedupe_idx;
