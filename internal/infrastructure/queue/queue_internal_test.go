package queue

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/domain"
)

type memStorage struct{ objs map[string][]byte }

func (m *memStorage) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	m.objs[key] = b
	return err
}

func (m *memStorage) Get(_ context.Context, key string) (application.Object, error) {
	b, ok := m.objs[key]
	if !ok {
		return application.Object{}, domain.ErrNotFound
	}
	return application.Object{Body: io.NopCloser(bytes.NewReader(b)), Size: int64(len(b))}, nil
}

func TestWithIDBadPayloadSkipsRetry(t *testing.T) {
	h := withID(func(context.Context, int) error { t.Fatal("job ran"); return nil })
	err := h(context.Background(), asynq.NewTask(TypePublishPost, []byte("not json")))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("err = %v, want SkipRetry", err)
	}
}

func TestInvalidatingOnlyAfterSuccess(t *testing.T) {
	calls := 0
	wrap := invalidating(func(context.Context) error { calls++; return errors.New("redis down") })
	ok := wrap(func(context.Context, int) error { return nil })
	if err := ok(context.Background(), 1); err != nil {
		t.Fatalf("invalidate error leaked: %v", err)
	}
	failed := wrap(func(context.Context, int) error { return errors.New("boom") })
	if err := failed(context.Background(), 1); err == nil {
		t.Fatal("job error swallowed")
	}
	if calls != 1 {
		t.Fatalf("invalidate calls = %d, want 1", calls)
	}
}

func TestThumbnail(t *testing.T) {
	ctx := context.Background()
	st := &memStorage{objs: map[string][]byte{"bad": []byte("not an image")}}

	if err := thumbnail(ctx, st, "missing"); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("missing: err = %v, want SkipRetry", err)
	}
	if err := thumbnail(ctx, st, "bad"); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("bad: err = %v, want SkipRetry", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for x := 0; x < 64; x++ {
		img.Set(x, x%48, color.RGBA{R: 200, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	st.objs["ok.png"] = buf.Bytes()
	if err := thumbnail(ctx, st, "ok.png"); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if _, ok := st.objs[application.ThumbKey("ok.png")]; !ok {
		t.Error("thumbnail not stored")
	}
	for _, w := range application.VariantWidths {
		if _, ok := st.objs[application.VariantKey("ok.png", w)]; !ok {
			t.Errorf("variant %d not stored", w)
		}
	}
}
