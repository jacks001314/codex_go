package model

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Rust parity: the spans codex-core instruments its model client with
// (core/src/session/turn.rs: `stream_request`, `receiving_stream`,
// `handle_responses`, `receiving`) and the fields
// SessionTelemetry::record_responses records on each per-event span.
//
// The model package cannot import codex_go/telemetry (telemetry reaches back
// here), so the span sink is declared locally; telemetry's SessionTelemetry
// satisfies it and opens the spans on the provider's tracer.

// TelemetrySpan is one open span the client instrumentation reports to.
type TelemetrySpan interface {
	// End closes the span.
	End()
	// Record sets attributes on the open span (Rust's `span.record`).
	Record(attributes map[string]string)
	// SetName applies Rust's `otel.name` override.
	SetName(name string)
	// TraceContext reports the span's W3C carrier, so callers crossing a process
	// boundary (code mode) can propagate the span context Rust reads from the
	// current span.
	TraceContext() (traceparent string, tracestate string, ok bool)
}

// Span names mirror Rust's client instrumentation.
const (
	StreamRequestSpanName   = "stream_request"
	ReceivingStreamSpanName = "receiving_stream"
	HandleResponsesSpanName = "handle_responses"
	ReceivingSpanName       = "receiving"
)

// telemetryTracer starts the client spans; a runner without a sink uses the
// no-op tracer so the instrumentation stays uniform.
type telemetryTracer interface {
	StartSpan(ctx context.Context, parent TelemetrySpan, name string, attributes map[string]string) (context.Context, TelemetrySpan)
}

// telemetryTracerFor returns the tracer of a sink, or the no-op tracer when the
// caller has no session telemetry (the bare parse helpers used by tests).
func telemetryTracerFor(sink SessionTelemetrySink) telemetryTracer {
	if sink != nil {
		return sink
	}
	return noopTelemetryTracer{}
}

// telemetryTracerFor returns the runner's span sink, or the no-op tracer.
func (r *ResponsesAgentRunner) telemetryTracerFor() telemetryTracer {
	if r != nil && r.Telemetry != nil {
		return r.Telemetry
	}
	return noopTelemetryTracer{}
}

type noopTelemetryTracer struct{}

type noopTelemetrySpan struct{}

func (noopTelemetryTracer) StartSpan(ctx context.Context, _ TelemetrySpan, _ string, _ map[string]string) (context.Context, TelemetrySpan) {
	return ctx, noopTelemetrySpan{}
}

func (noopTelemetrySpan) End()                     {}
func (noopTelemetrySpan) Record(map[string]string) {}
func (noopTelemetrySpan) SetName(string)           {}

func (noopTelemetrySpan) TraceContext() (string, string, bool) { return "", "", false }

// handleResponsesSpanAttributes mirrors the fields Rust creates the
// `handle_responses` span with: the effective reasoning effort plus the fields
// record_responses fills in while the event is handled.
func handleResponsesSpanAttributes(request *AgentRequest) map[string]string {
	effort := ""
	if request != nil {
		effort = strings.TrimSpace(request.ReasoningEffort)
	}
	if effort == "" {
		effort = "default"
	}
	return map[string]string{"codex.request.reasoning_effort": effort}
}

// recordResponsesSpan mirrors SessionTelemetry::record_responses: the span name
// (Rust's `otel.name`) for the event, and the from / tool_name / usage fields
// the event carries.
func recordResponsesSpan(span TelemetrySpan, event *ResponsesStreamEvent) {
	if span == nil || event == nil {
		return
	}
	if name := responsesTypeName(event); name != "" {
		span.SetName(name)
	}
	switch event.Kind {
	case ResponsesStreamEventOutputAdded:
		span.Record(responsesItemSpanFields(event, "output_item_added"))
	case ResponsesStreamEventOutputDone:
		span.Record(responsesItemSpanFields(event, "output_item_done"))
	case ResponsesStreamEventCompleted:
		if event.Usage == nil {
			return
		}
		usage := event.Usage
		span.Record(map[string]string{
			"gen_ai.usage.input_tokens":             strconv.FormatInt(usage.InputTokens, 10),
			"gen_ai.usage.cache_read.input_tokens":  strconv.FormatInt(maxInt64(usage.CachedInputTokens, 0), 10),
			"gen_ai.usage.cache_write.input_tokens": strconv.FormatInt(usage.CacheWriteInputTokens, 10),
			"gen_ai.usage.output_tokens":            strconv.FormatInt(usage.OutputTokens, 10),
			"codex.usage.reasoning_output_tokens":   strconv.FormatInt(usage.ReasoningOutputTokens, 10),
			"codex.usage.total_tokens":              strconv.FormatInt(usage.TotalTokens, 10),
		})
	}
}

// responsesTypeName mirrors SessionTelemetry::responses_type: the span name a
// streamed event reports. Go's stream yields a few notification events Rust's
// ResponseEvent enum does not have, which report no name.
// responsesItemSpanFields mirrors record_responses' OutputItemAdded and
// OutputItemDone arms: the event source, plus the tool name for function calls.
func responsesItemSpanFields(event *ResponsesStreamEvent, from string) map[string]string {
	fields := map[string]string{"from": from}
	if event != nil && event.Item != nil && strings.TrimSpace(event.Item.Type) == "function_call" {
		fields["tool_name"] = event.Item.Name
	}
	return fields
}

func responsesTypeName(event *ResponsesStreamEvent) string {
	if event == nil {
		return ""
	}
	switch event.Kind {
	case ResponsesStreamEventCreated:
		return "created"
	case ResponsesStreamEventOutputAdded, ResponsesStreamEventOutputDone:
		return responsesItemTypeName(event)
	case ResponsesStreamEventCompleted:
		return "completed"
	case ResponsesStreamEventOutputText:
		return "text_delta"
	case ResponsesStreamEventToolInputDelta:
		return "tool_input_delta"
	case ResponsesStreamEventReasoningSummaryTextDelta:
		return "reasoning_summary_delta"
	case ResponsesStreamEventReasoningTextDelta:
		return "reasoning_content_delta"
	case ResponsesStreamEventReasoningSummaryPartAdded:
		return "reasoning_summary_part_added"
	case ResponsesStreamEventServerModel:
		return "server_model"
	case ResponsesStreamEventModelVerify:
		return "model_verifications"
	case ResponsesStreamEventModeration:
		return "turn_moderation_metadata"
	case ResponsesStreamEventSafetyBuffer:
		return "safety_buffering"
	case ResponsesStreamEventReasoning:
		return "server_reasoning_included"
	case ResponsesStreamEventRateLimits:
		return "rate_limits"
	case ResponsesStreamEventModelsETag:
		return "models_etag"
	default:
		return ""
	}
}

// responsesItemTypeName mirrors SessionTelemetry::responses_item_type.
func responsesItemTypeName(event *ResponsesStreamEvent) string {
	if event == nil {
		return "other"
	}
	if name := responsesRawItemTypeName(event.RawItem); name != "" {
		return name
	}
	item := event.Item
	if item == nil {
		return "other"
	}
	switch strings.TrimSpace(item.Type) {
	case "additional_tools":
		return "additional_tools"
	case "agent_message":
		// Go builds a Go-side agent_message item for a streamed assistant
		// message, which Rust parses as ResponseItem::Message.
		return "message_from_assistant"
	case "reasoning":
		return "reasoning"
	case "local_shell_call":
		return "local_shell_call"
	case "function_call":
		return "function_call"
	case "tool_search_call":
		return "tool_search_call"
	case "function_call_output":
		return "function_call_output"
	case "tool_search_output":
		return "tool_search_output"
	case "custom_tool_call":
		return "custom_tool_call"
	case "custom_tool_call_output":
		return "custom_tool_call_output"
	case "web_search_call":
		return "web_search_call"
	case "image_generation_call":
		return "image_generation_call"
	case "compaction":
		return "compaction"
	case "configuration_update":
		return "configuration_update"
	case "compaction_trigger":
		return "compaction_trigger"
	case "context_compaction":
		return "context_compaction"
	default:
		return "other"
	}
}

// responsesRawItemTypeName mirrors responses_item_type for the raw streamed item
// Rust parses, which keeps the wire type and role the Go item taxonomy folds
// together. An unrecognized shape reports no name so the caller can fall back to
// the Go item type.
func responsesRawItemTypeName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		Type string `json:"type"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	switch strings.TrimSpace(payload.Type) {
	case "":
		return ""
	case "message":
		role := strings.TrimSpace(payload.Role)
		if role == "" {
			role = "assistant"
		}
		return "message_from_" + role
	case "agent_message":
		return "agent_message"
	case "reasoning":
		return "reasoning"
	case "local_shell_call":
		return "local_shell_call"
	case "function_call":
		return "function_call"
	case "tool_search_call":
		return "tool_search_call"
	case "function_call_output":
		return "function_call_output"
	case "tool_search_output":
		return "tool_search_output"
	case "custom_tool_call":
		return "custom_tool_call"
	case "custom_tool_call_output":
		return "custom_tool_call_output"
	case "web_search_call":
		return "web_search_call"
	case "image_generation_call":
		return "image_generation_call"
	case "compaction":
		return "compaction"
	case "configuration_update":
		return "configuration_update"
	case "compaction_trigger":
		return "compaction_trigger"
	case "context_compaction":
		return "context_compaction"
	case "additional_tools":
		return "additional_tools"
	default:
		// Rust parses an unknown item type as ResponseItem::Other.
		return "other"
	}
}

func maxInt64(value int64, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}
