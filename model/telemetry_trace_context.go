package model

import (
	"context"
	"net/http"
	"strings"
)

type telemetryTraceCarrierKey struct{}

// WithTelemetryTraceCarrier stores a span's W3C trace carrier in ctx so lower
// layers can propagate it without depending on the telemetry implementation.
func WithTelemetryTraceCarrier(ctx context.Context, carrier TelemetrySpan) context.Context {
	if ctx == nil || carrier == nil {
		return ctx
	}
	return context.WithValue(ctx, telemetryTraceCarrierKey{}, carrier)
}

// TelemetryTraceCarrierFromContext returns the active trace carrier.
func TelemetryTraceCarrierFromContext(ctx context.Context) TelemetrySpan {
	if ctx == nil {
		return nil
	}
	carrier, _ := ctx.Value(telemetryTraceCarrierKey{}).(TelemetrySpan)
	return carrier
}

// InjectTraceHeadersFromContext writes the active W3C trace context into
// outbound headers. Request construction remains responsible for any fallback
// from process TRACEPARENT.
func InjectTraceHeadersFromContext(ctx context.Context, headers http.Header) bool {
	carrier := TelemetryTraceCarrierFromContext(ctx)
	if carrier == nil || headers == nil {
		return false
	}
	traceparent, tracestate, ok := carrier.TraceContext()
	traceparent = strings.TrimSpace(traceparent)
	if !ok || traceparent == "" {
		return false
	}
	headers.Set("traceparent", traceparent)
	if tracestate = strings.TrimSpace(tracestate); tracestate != "" {
		headers.Set("tracestate", tracestate)
	} else {
		headers.Del("tracestate")
	}
	return true
}
