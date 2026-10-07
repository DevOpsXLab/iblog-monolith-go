package redis_test

// Redis adapters against a real Redis. Skipped with -short or without Docker.

import (
	"context"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/redis"
)

func connect(t *testing.T) *goredis.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	rd, err := tcredis.Run(ctx, "redis:8-alpine")
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(rd) })
	url, err := rd.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := redis.Connect(ctx, "not a url"); err == nil {
		t.Error("bad URL accepted")
	}
	rdb, err := redis.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rdb.Close() })
	return rdb
}

func dayKeyToday() string { return "trending:" + time.Now().UTC().Format("20060102") }

func TestRedisAdapters(t *testing.T) {
	rdb := connect(t)
	ctx := context.Background()

	t.Run("reset tokens are single use and scoped by prefix", func(t *testing.T) {
		resets := redis.ResetTokens{RDB: rdb}
		verifies := redis.ResetTokens{RDB: rdb, Prefix: "verify"}
		tok, err := resets.Issue(ctx, 42, time.Minute)
		if err != nil || len(tok) != 64 {
			t.Fatalf("token %q, %v", tok, err)
		}
		if _, err := verifies.Consume(ctx, tok); err == nil {
			t.Error("token crossed prefixes")
		}
		if id, err := resets.Consume(ctx, tok); err != nil || id != 42 {
			t.Fatalf("consume = %d, %v", id, err)
		}
		if _, err := resets.Consume(ctx, tok); err == nil {
			t.Error("token reused")
		}
		// Only the hash is stored.
		if n, _ := rdb.Exists(ctx, "pwreset:"+tok).Result(); n != 0 {
			t.Error("raw token stored as key")
		}
		short, _ := resets.Issue(ctx, 1, 50*time.Millisecond)
		time.Sleep(150 * time.Millisecond)
		if _, err := resets.Consume(ctx, short); err == nil {
			t.Error("expired token accepted")
		}
	})

	t.Run("mfa challenge attempts", func(t *testing.T) {
		c := redis.MFAChallenges{RDB: rdb}
		tok, err := c.Issue(ctx, 7, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= redis.MaxMFAAttempts; i++ {
			if id, err := c.Attempt(ctx, tok); err != nil || id != 7 {
				t.Fatalf("attempt %d = %d, %v", i, id, err)
			}
		}
		if _, err := c.Attempt(ctx, tok); err == nil {
			t.Fatal("attempt past the per-challenge limit")
		}
		if err := c.Consume(ctx, tok); err == nil {
			t.Error("burnt challenge consumed")
		}
		if _, err := c.Attempt(ctx, "missing"); err == nil {
			t.Error("missing challenge accepted")
		}
		if n, _ := rdb.Exists(ctx, "mfa:missing").Result(); n != 0 {
			t.Error("attempt on missing challenge left a key")
		}

		// Per-user cap spans challenges: 5 used above, 5 more allowed.
		tok2, _ := c.Issue(ctx, 7, time.Minute)
		tok3, _ := c.Issue(ctx, 7, time.Minute)
		for range redis.MaxMFAUserAttempts - redis.MaxMFAAttempts {
			if _, err := c.Attempt(ctx, tok2); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.Attempt(ctx, tok3); err == nil {
			t.Error("per-user limit not enforced across challenges")
		}
		if ttl := rdb.TTL(ctx, "mfa:user:7").Val(); ttl <= 0 || ttl > redis.MFAUserWindow {
			t.Errorf("user window ttl = %v", ttl)
		}

		// Consume is one-shot.
		tok4, _ := c.Issue(ctx, 8, time.Minute)
		if err := c.Consume(ctx, tok4); err != nil {
			t.Fatal(err)
		}
		if err := c.Consume(ctx, tok4); err == nil {
			t.Error("challenge consumed twice")
		}
	})

	t.Run("views and trending", func(t *testing.T) {
		v := redis.Views{RDB: rdb}
		for _, hit := range []struct {
			post   int
			viewer string
		}{{1, "a"}, {1, "a"}, {1, "b"}, {2, "a"}, {3, "a"}, {3, "b"}, {3, "c"}} {
			if err := v.Hit(ctx, hit.post, hit.viewer); err != nil {
				t.Fatal(err)
			}
		}
		if n := v.Count(ctx, 1); n != 2 {
			t.Errorf("unique views = %d, want 2", n)
		}
		counts := v.Counts(ctx, []int{1, 2, 3, 99})
		if counts[1] != 2 || counts[2] != 1 || counts[3] != 3 || counts[99] != 0 {
			t.Errorf("counts = %v", counts)
		}
		top, err := v.Trending(ctx, 7, 2)
		if err != nil || len(top) != 2 || top[0] != 3 || top[1] != 1 {
			t.Fatalf("trending = %v, %v", top, err)
		}
		// Cached for a minute: a new hit does not reorder yet.
		for i := range 5 {
			v.Hit(ctx, 2, "x"+strconv.Itoa(i))
		}
		if again, _ := v.Trending(ctx, 7, 2); again[0] != 3 {
			t.Errorf("trending cache bypassed: %v", again)
		}
		today := time.Now()
		daily, err := v.Daily(ctx, 3, []time.Time{today.AddDate(0, 0, -1), today})
		if err != nil || daily[0] != 0 || daily[1] != 3 {
			t.Errorf("daily = %v, %v", daily, err)
		}

		// An empty result is cached too (marker element), so a hit after it
		// stays invisible until the cache expires.
		empty := redis.Views{RDB: rdb}
		rdb.Del(ctx, "trending:cache:1:5", dayKeyToday())
		if none, err := empty.Trending(ctx, 1, 5); err != nil || len(none) != 0 {
			t.Fatalf("empty trending = %v, %v", none, err)
		}
		empty.Hit(ctx, 77, "new-viewer")
		if none, _ := empty.Trending(ctx, 1, 5); len(none) != 0 {
			t.Errorf("empty result not cached: %v", none)
		}
		rdb.Del(ctx, "trending:cache:1:5")
		if got, _ := empty.Trending(ctx, 1, 5); len(got) != 1 || got[0] != 77 {
			t.Errorf("after cache drop = %v", got)
		}

	})

	t.Run("events", func(t *testing.T) {
		e := redis.Events{RDB: rdb}
		sctx, cancel := context.WithCancel(ctx)
		msgs, err := e.Subscribe(sctx, "chan:test")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Publish(ctx, "chan:test", map[string]int{"n": 1}); err != nil {
			t.Fatal(err)
		}
		if err := e.Publish(ctx, "chan:test", func() {}); err == nil {
			t.Error("unmarshalable payload published")
		}
		select {
		case m := <-msgs:
			if string(m) != `{"n":1}` {
				t.Errorf("message = %s", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no message")
		}
		cancel()
		select {
		case _, ok := <-msgs:
			if ok {
				t.Error("unexpected message after cancel")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("channel not closed after cancel")
		}
	})
}
