// Package tracehook carries a minimal tracing span across packages that cannot
// depend on the telemetry implementation without creating an import cycle.
package tracehook

import "context"

// Span is the tracing surface shared by low-level transport packages.
type Span interface {
	AddEvent(name string, attributes map[string]string)
	SetAttribute(key string, value string)
	End()
	StartChildSpan(name string, attributes map[string]string) Span
}

type spanContextKey struct{}

// WithSpan returns a context carrying the trace span.
func WithSpan(ctx context.Context, span Span) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if span == nil {
		return ctx
	}
	return context.WithValue(ctx, spanContextKey{}, span)
}

// SpanFromContext returns the span carried by ctx, or nil.
func SpanFromContext(ctx context.Context) Span {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(spanContextKey{}).(Span)
	return span
}
