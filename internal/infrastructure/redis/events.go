package redis

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// Events fans realtime messages out over Redis pub/sub, so every API
// instance can serve any SSE subscriber.
type Events struct{ RDB *redis.Client }

func (e Events) Publish(ctx context.Context, channel string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return e.RDB.Publish(ctx, channel, b).Err()
}

func (e Events) Subscribe(ctx context.Context, channel string) (<-chan []byte, error) {
	sub := e.RDB.Subscribe(ctx, channel)
	if _, err := sub.Receive(ctx); err != nil {
		sub.Close()
		return nil, err
	}
	out := make(chan []byte, 16)
	go func() {
		defer close(out)
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- []byte(m.Payload):
				default: // slow consumer: drop rather than block pub/sub
				}
			}
		}
	}()
	return out, nil
}
