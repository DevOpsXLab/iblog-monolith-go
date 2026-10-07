package redis

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// MFAChallenges are short-lived tokens between a correct password and the
// second factor. Each allows MaxAttempts code guesses.
type MFAChallenges struct{ RDB *redis.Client }

const (
	MaxMFAAttempts     = 5  // per challenge
	MaxMFAUserAttempts = 10 // per user per MFAUserWindow
	MFAUserWindow      = 15 * time.Minute
)

var errNoChallenge = errors.New("challenge not found")

func (MFAChallenges) key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "mfa:" + hex.EncodeToString(sum[:])
}

func (c MFAChallenges) Issue(ctx context.Context, userID int, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	k := c.key(token)
	_, err := c.RDB.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, k, "user", userID, "attempts", 0)
		p.Expire(ctx, k, ttl)
		return nil
	})
	return token, err
}

// Attempt counts one guess and returns the user id; the challenge is gone
// after MaxMFAAttempts guesses.
func (c MFAChallenges) Attempt(ctx context.Context, token string) (int, error) {
	k := c.key(token)
	n, err := c.RDB.HIncrBy(ctx, k, "attempts", 1).Result()
	if err != nil {
		return 0, err
	}
	v, err := c.RDB.HGet(ctx, k, "user").Result()
	if errors.Is(err, redis.Nil) || n > MaxMFAAttempts {
		c.RDB.Del(ctx, k) // HIncrBy on a missing key created it
		return 0, errNoChallenge
	}
	if err != nil {
		return 0, err
	}
	// Fresh challenges are cheap for whoever knows the password, so guesses
	// are also capped per user across challenges.
	uk := "mfa:user:" + v
	tries, err := c.RDB.Incr(ctx, uk).Result()
	if err != nil {
		return 0, err
	}
	if tries == 1 {
		c.RDB.Expire(ctx, uk, MFAUserWindow)
	}
	if tries > MaxMFAUserAttempts {
		return 0, errNoChallenge
	}
	return strconv.Atoi(v)
}

func (c MFAChallenges) Consume(ctx context.Context, token string) error {
	n, err := c.RDB.Del(ctx, c.key(token)).Result()
	if err == nil && n == 0 {
		return errNoChallenge // used concurrently
	}
	return err
}
