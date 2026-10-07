package redis

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Views counts unique viewers per post with HyperLogLog and ranks trending
// posts with one sorted set per day.
type Views struct{ RDB *redis.Client }

const trendingTTL = 31 * 24 * time.Hour

func viewsKey(postID int) string { return "views:post:" + strconv.Itoa(postID) }
func dayKey(t time.Time) string  { return "trending:" + t.UTC().Format("20060102") }

func (v Views) Hit(ctx context.Context, postID int, viewer string) error {
	added, err := v.RDB.PFAdd(ctx, viewsKey(postID), viewer).Result()
	if err != nil || added == 0 {
		return err
	}
	key := dayKey(time.Now())
	pipe := v.RDB.TxPipeline()
	pipe.ZIncrBy(ctx, key, 1, strconv.Itoa(postID))
	pipe.Expire(ctx, key, trendingTTL)
	_, err = pipe.Exec(ctx)
	return err
}

func (v Views) Count(ctx context.Context, postID int) int64 {
	n, _ := v.RDB.PFCount(ctx, viewsKey(postID)).Result()
	return n
}

// Counts returns unique views for many posts in one round trip.
func (v Views) Counts(ctx context.Context, postIDs []int) map[int]int64 {
	pipe := v.RDB.Pipeline()
	cmds := make([]*redis.IntCmd, len(postIDs))
	for i, id := range postIDs {
		cmds[i] = pipe.PFCount(ctx, viewsKey(id))
	}
	pipe.Exec(ctx) // per-command errors read below; missing keys count 0
	out := make(map[int]int64, len(postIDs))
	for i, c := range cmds {
		out[postIDs[i]], _ = c.Result()
	}
	return out
}

const trendingCacheTTL = time.Minute

// Trending ranks posts by unique views over the last days. Redis merges and
// ranks the daily sets; the result is cached for a minute.
func (v Views) Trending(ctx context.Context, days, limit int) ([]int, error) {
	cacheKey := "trending:cache:" + strconv.Itoa(days) + ":" + strconv.Itoa(limit)
	if cached, err := v.RDB.LRange(ctx, cacheKey, 0, -1).Result(); err == nil && len(cached) > 0 {
		return atois(cached[1:]), nil // [0] is a marker so empty results cache too
	}
	keys := make([]string, days)
	for i := range days {
		keys[i] = dayKey(time.Now().AddDate(0, 0, -i))
	}
	tmp := "trending:tmp:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	pipe := v.RDB.TxPipeline()
	pipe.ZUnionStore(ctx, tmp, &redis.ZStore{Keys: keys})
	top := pipe.ZRevRange(ctx, tmp, 0, int64(limit-1))
	pipe.Del(ctx, tmp)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	ids := top.Val()
	cache := v.RDB.TxPipeline()
	cache.Del(ctx, cacheKey)
	cache.RPush(ctx, cacheKey, append([]any{"*"}, toAny(ids)...)...)
	cache.Expire(ctx, cacheKey, trendingCacheTTL)
	cache.Exec(ctx) // best effort
	return atois(ids), nil
}

func atois(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if id, err := strconv.Atoi(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Daily returns the post's first-time unique viewers on each day (a viewer
// counts once per post, ever; from the trending sets, so at most the last
// 31 days).
func (v Views) Daily(ctx context.Context, postID int, days []time.Time) ([]int64, error) {
	pipe := v.RDB.Pipeline()
	cmds := make([]*redis.FloatCmd, len(days))
	for i, d := range days {
		cmds[i] = pipe.ZScore(ctx, dayKey(d), strconv.Itoa(postID))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := make([]int64, len(days))
	for i, c := range cmds {
		n, _ := c.Result()
		out[i] = int64(n)
	}
	return out, nil
}
