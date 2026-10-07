package telemetry

import (
	"context"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger builds the app logger: JSON on stdout and, with OTel enabled,
// the OTLP log pipeline too. A context.Context field (zap.Any("ctx", ctx))
// links the record to the active span: stdout gets trace_id/span_id, OTLP
// gets the span context.
func NewLogger() *zap.Logger {
	enc := zap.NewProductionEncoderConfig()
	enc.TimeKey = "time"
	enc.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	var core zapcore.Core = traceCore{zapcore.NewCore(zapcore.NewJSONEncoder(enc), zapcore.Lock(os.Stdout), zap.InfoLevel)}
	if Enabled() {
		core = zapcore.NewTee(core, otelzap.NewCore(ServiceName))
	}
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
}

// traceCore swaps context fields for trace_id/span_id so JSON output stays
// readable and correlates with traces.
type traceCore struct{ zapcore.Core }

func (c traceCore) With(fs []zapcore.Field) zapcore.Core {
	return traceCore{c.Core.With(withTrace(fs))}
}

func (c traceCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

func (c traceCore) Write(e zapcore.Entry, fs []zapcore.Field) error {
	return c.Core.Write(e, withTrace(fs))
}

func withTrace(fs []zapcore.Field) []zapcore.Field {
	out := fs[:0:0]
	for _, f := range fs {
		ctx, ok := f.Interface.(context.Context)
		if !ok {
			out = append(out, f)
			continue
		}
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			out = append(out, zap.String("trace_id", sc.TraceID().String()), zap.String("span_id", sc.SpanID().String()))
		}
	}
	return out
}
