// Package telemetry sets up OpenTelemetry traces, metrics and logs.
package telemetry

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// ServiceName is used when OTEL_SERVICE_NAME is not set.
const ServiceName = "iblog-api"

// Enabled reports whether OTLP export is configured.
func Enabled() bool { return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" }

// Setup enables OTLP/HTTP export of traces, metrics and logs when
// OTEL_EXPORTER_OTLP_ENDPOINT is set; standard OTEL_* env vars (sampler,
// headers, resource attributes) apply; sampling: see sampler. It returns a shutdown func that
// flushes pending data.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !Enabled() {
		return noop, nil
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(ServiceName)),
		resource.WithFromEnv(), // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES win
		resource.WithHost(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return noop, err
	}

	traceExp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExp), sdktrace.WithResource(res), sdktrace.WithSampler(sampler()))

	metricExp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return noop, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	)

	logExp, err := otlploghttp.New(ctx)
	if err != nil {
		return noop, err
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)), sdklog.WithResource(res))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	global.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	if err := runtime.Start(); err != nil {
		return noop, err
	}
	// Every outbound HTTP client using the default transport gets client spans.
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx), lp.Shutdown(ctx))
	}, nil
}

// sampler is parent-based with OTEL_TRACES_SAMPLER_ARG as the root ratio
// (default 1), and drops root client spans: asynq polls Redis every second,
// and those commands would each become a one-span trace.
func sampler() sdktrace.Sampler {
	ratio := 1.0
	if v, err := strconv.ParseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 64); err == nil {
		ratio = v
	}
	return dropOrphanClients{sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))}
}

type dropOrphanClients struct{ sdktrace.Sampler }

func (s dropOrphanClients) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if p.Kind == trace.SpanKindClient && !trace.SpanContextFromContext(p.ParentContext).IsValid() {
		return sdktrace.SamplingResult{Decision: sdktrace.Drop}
	}
	return s.Sampler.ShouldSample(p)
}

func (s dropOrphanClients) Description() string {
	return "DropOrphanClients{" + s.Sampler.Description() + "}"
}

var tracer = otel.Tracer("github.com/iBlog/iblog-monolith-go")

// Start starts an internal span.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, opts...)
}

// End records err (if any) on span and ends it. Use with a named error:
//
//	ctx, span := telemetry.Start(ctx, "x")
//	defer func() { telemetry.End(span, err) }()
func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
