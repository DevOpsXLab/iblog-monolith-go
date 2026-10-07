---
title: OpenTelemetry bilan tanishuv: trace, metric va log bir joyda
subtitle: Go servisini instrumentatsiya qilib, Grafana LGTM'da ko'ramiz
author: nodira
category: Observability
tags: opentelemetry, observability, grafana, tracing
labels: Qo'llanma, Yangi boshlovchilar
publication: cloud-native-uz
days_ago: 70
cover: dashboard-graphs
---
"Sayt sekin ishlayapti" degan shikoyat keldi. Qaysi servis? Qaysi so'rov? Bazami yoki tashqi API? Uchta alohida vosita va qo'lda vaqt solishtirish bilan bu savollarga javob topish soatlab vaqt oladi.

OpenTelemetry (OTel) — trace, metric va log'lar uchun vendor-neytral standart. Bir marta instrumentatsiya qilasiz, keyin istalgan backend'ga yuborasiz.

## Lokal stek bir buyruqda

```bash
docker run -d --name lgtm -p 3000:3000 -p 4317:4317 -p 4318:4318 grafana/otel-lgtm
```

Ichida Grafana, Tempo (trace), Prometheus (metric), Loki (log) va OTel Collector.

## Go servisini ulash

```go
import (
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func setupTracing(ctx context.Context) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx) // OTEL_EXPORTER_OTLP_ENDPOINT dan o'qiydi
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

handler := otelhttp.NewHandler(mux, "api")
```

pgx uchun `otelpgx`, Redis uchun `redisotel` — har bir SQL so'rov trace ichida alohida span bo'lib ko'rinadi.

## Log'ni trace bilan bog'lash

Eng foydali qadam: har bir log yozuviga `trace_id` qo'shish. Grafana'da log'dan bir bosishda to'liq trace'ga o'tasiz.

```go
span := trace.SpanFromContext(ctx)
logger.Info("order created",
	zap.String("trace_id", span.SpanContext().TraceID().String()),
	zap.Int("order_id", id))
```

## Sampling

Prod'da har bir so'rovni yozish qimmat. Boshlash uchun:

```bash
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.1
```

Keyinroq Collector'da tail sampling: barcha xatoli va sekin trace'lar saqlanadi, qolganining 5 foizi.

## Uchta oltin signal

Har bir servis uchun dashboard'da kamida: so'rovlar soni (rate), xatolar ulushi (errors), p50/p95/p99 kechikish (duration). RED metodi — alertlarning asosi.
