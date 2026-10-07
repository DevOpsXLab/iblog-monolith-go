package http

import (
	"context"
	"testing"
)

func TestShuttingDown(t *testing.T) {
	if shuttingDown(context.Background()) != nil {
		t.Fatal("plain context reports a shutdown channel")
	}
	done, stop := context.WithCancel(context.Background())
	ctx := WithShutdown(context.Background(), done)
	ch := shuttingDown(ctx)
	select {
	case <-ch:
		t.Fatal("closed before shutdown")
	default:
	}
	stop()
	<-ch
	if ctx.Err() != nil {
		t.Error("request context cancelled with the server: in-flight requests must finish")
	}
}
