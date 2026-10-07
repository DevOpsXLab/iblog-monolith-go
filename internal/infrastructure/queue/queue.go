// Package queue runs background jobs on Redis with asynq: scheduled
// publishing, follower fan-out, account purges, email and thumbnails.
package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/textproto"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/application"
	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/user"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/images"
	"github.com/iBlog/iblog-monolith-go/internal/infrastructure/telemetry"
	"github.com/iBlog/iblog-monolith-go/internal/interfaces/http/middleware"
)

// Task types.
const (
	TypePublishPost  = "post:publish"
	TypeFanoutPost   = "post:fanout"
	TypePurgeAccount = "account:purge"
	TypeSendEmail    = "email:send"
	TypeThumbnail    = "image:thumbnail"
	TypeSweep        = "sweep" // periodic: overdue publishing, purges, expired suspensions
)

type idPayload struct {
	ID int `json:"id"`
}

type keyPayload struct {
	Key string `json:"key"`
}

// Client implements application.Jobs.
type Client struct{ c *asynq.Client }

func NewClient(rdb *redis.Client) *Client { return &Client{c: asynq.NewClientFromRedisClient(rdb)} }

func (c *Client) Close() error { return c.c.Close() }

func (c *Client) enqueue(ctx context.Context, typ string, payload any, opts ...asynq.Option) (err error) {
	ctx, span := telemetry.Start(ctx, "asynq.enqueue "+typ, trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("messaging.system", "asynq"), attribute.String("messaging.operation.name", typ)))
	defer func() { telemetry.End(span, err) }()
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if b, err = wrap(ctx, b); err != nil {
		return err
	}
	info, err := c.c.EnqueueContext(ctx, asynq.NewTask(typ, b), opts...)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		span.SetAttributes(attribute.Bool("asynq.duplicate", true))
		return nil // already scheduled
	}
	if err == nil {
		span.SetAttributes(attribute.String("messaging.message.id", info.ID), attribute.String("asynq.queue", info.Queue))
	}
	return err
}

// envelope carries the trace context of the enqueuing request so the job's
// span joins the same trace.
type envelope struct {
	Otel map[string]string `json:"_otel"`
	Data json.RawMessage   `json:"_data"`
}

func wrap(ctx context.Context, data []byte) ([]byte, error) {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return json.Marshal(envelope{Otel: carrier, Data: data})
}

// traced unwraps the envelope (tasks without one, e.g. scheduler tasks or
// ones queued before tracing, pass through) and runs the handler in a
// consumer span.
func traced(next asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) (err error) {
		var env envelope
		if json.Unmarshal(t.Payload(), &env) == nil && env.Data != nil {
			ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(env.Otel))
			t = asynq.NewTask(t.Type(), env.Data)
		}
		attrs := []attribute.KeyValue{attribute.String("messaging.system", "asynq"), attribute.String("messaging.operation.name", t.Type())}
		if id, ok := asynq.GetTaskID(ctx); ok {
			attrs = append(attrs, attribute.String("messaging.message.id", id))
		}
		if n, ok := asynq.GetRetryCount(ctx); ok {
			attrs = append(attrs, attribute.Int("asynq.retry_count", n))
		}
		ctx, span := telemetry.Start(ctx, "asynq.process "+t.Type(), trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attrs...))
		defer func() { telemetry.End(span, err) }()
		return next.ProcessTask(ctx, t)
	})
}

func (c *Client) PublishPostAt(ctx context.Context, postID int, at time.Time) error {
	// One task per (post, time): rescheduling adds a new task; the stale one
	// finds the post not yet due (or already published) and does nothing.
	id := fmt.Sprintf("publish:%d:%d", postID, at.Unix())
	return c.enqueue(ctx, TypePublishPost, idPayload{postID}, asynq.ProcessAt(at), asynq.TaskID(id))
}

func (c *Client) FanoutNewPost(ctx context.Context, postID int) error {
	return c.enqueue(ctx, TypeFanoutPost, idPayload{postID}, asynq.TaskID("fanout:"+strconv.Itoa(postID)), asynq.Retention(24*time.Hour))
}

func (c *Client) PurgeAccountAt(ctx context.Context, userID int, at time.Time) error {
	id := fmt.Sprintf("purge:%d:%d", userID, at.Unix())
	return c.enqueue(ctx, TypePurgeAccount, idPayload{userID}, asynq.ProcessAt(at), asynq.TaskID(id))
}

func (c *Client) SendEmail(ctx context.Context, m application.Email) error {
	return c.enqueue(ctx, TypeSendEmail, m, asynq.MaxRetry(5))
}

func (c *Client) SendEmailOnce(ctx context.Context, id string, m application.Email) error {
	return c.enqueue(ctx, TypeSendEmail, m, asynq.MaxRetry(5), asynq.TaskID("email:"+id), asynq.Retention(7*24*time.Hour))
}

func (c *Client) Thumbnail(ctx context.Context, key string) error {
	return c.enqueue(ctx, TypeThumbnail, keyPayload{key})
}

// Worker processes tasks and runs the periodic sweep.
type Worker struct {
	srv   *asynq.Server
	sched *asynq.Scheduler
	mux   *asynq.ServeMux
}

// Handlers are the application services the worker calls.
type Handlers struct {
	Blog     *application.Blog
	Accounts *application.Accounts
	Mailer   application.Mailer
	Storage  application.Storage
	// Analytics purges old events in the sweep (nil: nothing to purge).
	Analytics *application.Analytics
	// InvalidateCache drops cached HTTP responses after a job changed public
	// data (nil: middleware.ResetCache on the worker's Redis).
	InvalidateCache func(context.Context) error
}

// SweepEvery is how often overdue publishing and purges are retried.
const SweepEvery = "@every 1m"

func NewWorker(rdb *redis.Client, h Handlers) *Worker {
	if h.InvalidateCache == nil {
		h.InvalidateCache = func(ctx context.Context) error { return middleware.ResetCache(ctx, rdb) }
	}
	inv := invalidating(h.InvalidateCache)
	mux := asynq.NewServeMux()
	mux.Use(traced)
	mux.HandleFunc(TypePublishPost, withID(inv(h.Blog.PublishScheduled)))
	mux.HandleFunc(TypeFanoutPost, withID(h.Blog.FanoutNewPost))
	mux.HandleFunc(TypePurgeAccount, withID(inv(h.Accounts.Purge)))
	mux.HandleFunc(TypeSendEmail, func(ctx context.Context, t *asynq.Task) error {
		var m application.Email
		if err := json.Unmarshal(t.Payload(), &m); err != nil {
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		id, _ := asynq.GetTaskID(ctx)
		return sendEmail(ctx, redisSentLog{rdb}, h.Mailer, id, m)
	})
	mux.HandleFunc(TypeThumbnail, func(ctx context.Context, t *asynq.Task) error {
		var p keyPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		return thumbnail(ctx, h.Storage, p.Key)
	})
	mux.HandleFunc(TypeSweep, func(ctx context.Context, _ *asynq.Task) error {
		// The sweep runs every minute; invalidate only when it had work,
		// otherwise the response cache would never outlive a minute.
		work := sweepHasWork(ctx, h)
		err := errors.Join(h.Blog.PublishDue(ctx), h.Accounts.PurgeDue(ctx), h.Accounts.LiftExpired(ctx), h.Analytics.PurgeOld(ctx))
		if work {
			invalidate(ctx, h.InvalidateCache) // partial success still changed data
		}
		return err
	})

	srv := asynq.NewServerFromRedisClient(rdb, asynq.Config{
		Concurrency: 5,
		Logger:      zapLogger{},
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			zap.L().Error("job failed", zap.Any("ctx", ctx), zap.Any("type", t.Type()), zap.Error(err))
		}),
	})
	sched := asynq.NewSchedulerFromRedisClient(rdb, &asynq.SchedulerOpts{Logger: zapLogger{}})
	return &Worker{srv: srv, sched: sched, mux: mux}
}

func (w *Worker) Start() error {
	if _, err := w.sched.Register(SweepEvery, asynq.NewTask(TypeSweep, nil), asynq.Unique(time.Minute)); err != nil {
		return err
	}
	if err := w.sched.Start(); err != nil {
		return err
	}
	return w.srv.Start(w.mux)
}

// Stop waits for running tasks to finish.
func (w *Worker) Stop() {
	w.sched.Shutdown()
	w.srv.Shutdown()
}

func withID(fn func(context.Context, int) error) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p idPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		return fn(ctx, p.ID)
	}
}

// invalidating wraps a job so a successful run drops cached responses.
func invalidating(fn func(context.Context) error) func(func(context.Context, int) error) func(context.Context, int) error {
	return func(job func(context.Context, int) error) func(context.Context, int) error {
		return func(ctx context.Context, id int) error {
			if err := job(ctx, id); err != nil {
				return err
			}
			invalidate(ctx, fn)
			return nil
		}
	}
}

// invalidate logs instead of failing: the write is done, and stale entries
// still expire by TTL.
func invalidate(ctx context.Context, fn func(context.Context) error) {
	if err := fn(ctx); err != nil {
		zap.L().Warn("cache invalidate", zap.Any("ctx", ctx), zap.Error(err))
	}
}

// sweepHasWork reports whether the sweep may change data; on lookup errors
// it assumes yes.
func sweepHasWork(ctx context.Context, h Handlers) bool {
	now := time.Now()
	if ids, err := h.Blog.Posts.DueScheduled(ctx, now); err != nil || len(ids) > 0 {
		return true
	}
	if ids, err := h.Blog.Users.DuePurges(ctx, now.Add(-user.DeletionGrace)); err != nil || len(ids) > 0 {
		return true
	}
	if h.Accounts.Sanctions != nil {
		if ids, err := h.Accounts.Sanctions.Expired(ctx, now); err != nil || len(ids) > 0 {
			return true
		}
	}
	return false
}

// sentLog remembers which email tasks were delivered. The task ID is stable
// across retries, so a retry after a delivered-but-unacknowledged send (the
// worker died, or the run failed after the server took the message) finds
// the mark and does not send again.
type sentLog interface {
	Sent(ctx context.Context, id string) (bool, error)
	MarkSent(ctx context.Context, id string) error
}

// sentTTL outlives every retry (MaxRetry 5 with asynq's backoff ends within
// days) and the SendEmailOnce retention.
const sentTTL = 8 * 24 * time.Hour

type redisSentLog struct{ rdb *redis.Client }

func (l redisSentLog) Sent(ctx context.Context, id string) (bool, error) {
	n, err := l.rdb.Exists(ctx, "email:sent:"+id).Result()
	return n > 0, err
}

func (l redisSentLog) MarkSent(ctx context.Context, id string) error {
	return l.rdb.Set(ctx, "email:sent:"+id, 1, sentTTL).Err()
}

func sendEmail(ctx context.Context, log sentLog, mailer application.Mailer, id string, m application.Email) error {
	if id == "" { // no stable id: nothing to dedupe on
		return classifyMailErr(mailer.Send(ctx, m))
	}
	sent, err := log.Sent(ctx, id)
	if err != nil {
		return fmt.Errorf("email sent check: %w", err) // retry later rather than risk a duplicate
	}
	if sent {
		zap.L().Info("email already sent, skipping retry", zap.Any("ctx", ctx), zap.String("task", id))
		return nil
	}
	if err := mailer.Send(ctx, m); err != nil {
		return classifyMailErr(err)
	}
	// The mail is out: failing the task now would only send it again.
	if err := log.MarkSent(ctx, id); err != nil {
		zap.L().Warn("email mark sent", zap.Any("ctx", ctx), zap.String("task", id), zap.Error(err))
	}
	return nil
}

// classifyMailErr stops retries on permanent SMTP rejections (5xx: unknown
// mailbox, policy): a retry gets the same answer.
func classifyMailErr(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 500 {
		return fmt.Errorf("%w: %w", asynq.SkipRetry, err)
	}
	return err
}

func thumbnail(ctx context.Context, st application.Storage, key string) error {
	obj, err := st.Get(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	if err != nil {
		return err
	}
	defer obj.Body.Close()
	_, span := telemetry.Start(ctx, "images.Process", trace.WithAttributes(attribute.String("object.key", key)))
	p, err := images.Process(obj.Body, application.VariantWidths)
	telemetry.End(span, err)
	if err != nil { // bad or oversized (images.ErrTooLarge) image: retrying cannot help
		return fmt.Errorf("%w: decode %s: %w", asynq.SkipRetry, key, err)
	}
	for _, w := range application.VariantWidths {
		v := p.Variants[w]
		if err := st.Put(ctx, application.VariantKey(key, w), bytes.NewReader(v), int64(len(v)), "image/webp"); err != nil {
			return err
		}
	}
	// The thumbnail goes last: clients poll it to know every file is ready.
	return st.Put(ctx, application.ThumbKey(key), bytes.NewReader(p.Thumbnail), int64(len(p.Thumbnail)), "image/jpeg")
}

// zapLogger routes asynq logs to zap.
type zapLogger struct{}

func (zapLogger) log() *zap.SugaredLogger { return zap.L().Sugar().With("component", "queue") }
func (l zapLogger) Debug(args ...any)     {}
func (l zapLogger) Info(args ...any)      { l.log().Info(args...) }
func (l zapLogger) Warn(args ...any)      { l.log().Warn(args...) }
func (l zapLogger) Error(args ...any)     { l.log().Error(args...) }
func (l zapLogger) Fatal(args ...any)     { l.log().Error(args...) } // never exit from a library callback
