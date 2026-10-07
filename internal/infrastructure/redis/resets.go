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

// ResetTokens stores single-use tokens hashed: password resets by default,
// or another purpose (e.g. email verification) under Prefix.
type ResetTokens struct {
	RDB    *redis.Client
	Prefix string // default "pwreset"
}

func (t ResetTokens) key(token string) string {
	prefix := t.Prefix
	if prefix == "" {
		prefix = "pwreset"
	}
	sum := sha256.Sum256([]byte(token))
	return prefix + ":" + hex.EncodeToString(sum[:])
}

func (t ResetTokens) Issue(ctx context.Context, userID int, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	return token, t.RDB.Set(ctx, t.key(token), userID, ttl).Err()
}

func (t ResetTokens) Consume(ctx context.Context, token string) (int, error) {
	v, err := t.RDB.GetDel(ctx, t.key(token)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, errors.New("token not found")
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}
