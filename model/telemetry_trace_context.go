package model

import (
	"context"
	"net/http"
	"strings"

	"codex_go/protocol"
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
// outbound headers. The active span carrier wins; when no tracing layer is
// installed the inbound request carrier stored by the app server is used
// instead (Rust #60506: MCP requests keep their request trace context).
func InjectTraceHeadersFromContext(ctx context.Context, headers http.Header) bool {
	if headers == nil {
		return false
	}
	if carrier := TelemetryTraceCarrierFromContext(ctx); carrier != nil {
		traceparent, tracestate, ok := carrier.TraceContext()
		traceparent = strings.TrimSpace(traceparent)
		if ok && traceparent != "" {
			setTraceHeaders(headers, traceparent, tracestate)
			return true
		}
	}
	trace, ok := protocol.TraceContextFromContext(ctx)
	if !ok || trace == nil {
		return false
	}
	traceparent := strings.TrimSpace(trace.Traceparent)
	if traceparent == "" {
		return false
	}
	setTraceHeaders(headers, traceparent, trace.Tracestate)
	return true
}

func setTraceHeaders(headers http.Header, traceparent string, tracestate string) {
	headers.Set("traceparent", traceparent)
	if tracestate = strings.TrimSpace(tracestate); tracestate != "" {
		headers.Set("tracestate", tracestate)
	} else {
		headers.Del("tracestate")
	}
}
