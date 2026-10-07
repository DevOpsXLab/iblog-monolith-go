package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSetupDisabledIsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if Enabled() {
		t.Fatal("enabled without endpoint")
	}
	shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func params(kind trace.SpanKind, parent context.Context) sdktrace.SamplingParameters {
	var tid trace.TraceID
	tid[0] = 1
	return sdktrace.SamplingParameters{ParentContext: parent, TraceID: tid, Name: "x", Kind: kind}
}

func parentCtx() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestSamplerDropsOrphanClientSpans(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	s := sampler()
	if !strings.HasPrefix(s.Description(), "DropOrphanClients{") {
		t.Errorf("description = %q", s.Description())
	}
	cases := []struct {
		name   string
		kind   trace.SpanKind
		parent context.Context
		want   sdktrace.SamplingDecision
	}{
		{"root client (redis poll)", trace.SpanKindClient, context.Background(), sdktrace.Drop},
		{"client under a request", trace.SpanKindClient, parentCtx(), sdktrace.RecordAndSample},
		{"root server", trace.SpanKindServer, context.Background(), sdktrace.RecordAndSample},
	}
	for _, c := range cases {
		if got := s.ShouldSample(params(c.kind, c.parent)).Decision; got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSamplerRatio(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	if got := sampler().ShouldSample(params(trace.SpanKindServer, context.Background())).Decision; got != sdktrace.Drop {
		t.Errorf("ratio 0 root = %v", got)
	}
	// A sampled parent still wins.
	if got := sampler().ShouldSample(params(trace.SpanKindServer, parentCtx())).Decision; got != sdktrace.RecordAndSample {
		t.Errorf("ratio 0 child = %v", got)
	}
}

func TestTraceCoreSwapsContextForIDs(t *testing.T) {
	obs, logs := observer.New(zap.InfoLevel)
	log := zap.New(traceCore{obs})

	ctx := parentCtx()
	log.Info("with span", zap.Any("ctx", ctx), zap.Int("n", 1))
	log.With(zap.Any("ctx", context.Background())).Info("no span")
	log.Debug("filtered")

	all := logs.All()
	if len(all) != 2 {
		t.Fatalf("entries = %d, want 2", len(all))
	}
	f := all[0].ContextMap()
	sc := trace.SpanContextFromContext(ctx)
	if f["trace_id"] != sc.TraceID().String() || f["span_id"] != sc.SpanID().String() || f["n"] != int64(1) {
		t.Errorf("fields = %v", f)
	}
	if _, ok := f["ctx"]; ok {
		t.Error("raw ctx field kept")
	}
	if f := all[1].ContextMap(); len(f) != 0 {
		t.Errorf("no-span fields = %v", f)
	}
}

func TestNewLogger(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	l := NewLogger()
	if !l.Core().Enabled(zapcore.InfoLevel) || l.Core().Enabled(zapcore.DebugLevel) {
		t.Error("logger level is not info")
	}
}

func TestEndRecordsError(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	_, span := tp.Tracer("t").Start(context.Background(), "op")
	End(span, errors.New("boom"))
	_, ok := tp.Tracer("t").Start(context.Background(), "ok")
	End(ok, nil)

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	if spans[0].Status().Description != "boom" || len(spans[0].Events()) != 1 {
		t.Errorf("error span = %+v", spans[0].Status())
	}
	if spans[1].Status().Description != "" {
		t.Errorf("ok span status = %+v", spans[1].Status())
	}
}
