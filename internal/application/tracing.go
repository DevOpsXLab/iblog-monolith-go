package application

import "go.opentelemetry.io/otel"

// tracer gives every use case its own span, between the HTTP/job span and
// the storage spans.
var tracer = otel.Tracer("github.com/DevOpsXLab/iblog-monolith-go/internal/application")
