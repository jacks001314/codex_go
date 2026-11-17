package telemetry

import (
	"context"
	"log/slog"
)

// CodexPluginUsedEventType is Rust's AnalyticsFact::PluginUsed event name: the
// turn input named a plugin explicitly (an `@plugin` / `plugin://` mention).
const CodexPluginUsedEventType = "codex_plugin_used"

// CodexPluginUsedMetadata mirrors analytics/src/events.rs::CodexPluginUsedMetadata.
// The plugin identity fields are flattened as in Rust's `#[serde(flatten)]`, so
// the wire shape is turn_trigger, the CodexPluginMetadata fields,
// mcp_server_names, thread_id, turn_id and model_slug.
type CodexPluginUsedMetadata struct {
	TurnTrigger *string `json:"turn_trigger,omitempty"`
	CodexPluginMetadata
	MCPServerNames []string `json:"mcp_server_names"`
	ThreadID       *string  `json:"thread_id"`
	TurnID         *string  `json:"turn_id"`
	ModelSlug      *string  `json:"model_slug"`
}

// CodexPluginUsedEventRequest is the tracked request for one plugin use.
type CodexPluginUsedEventRequest struct {
	EventType   string                  `json:"event_type"`
	EventParams CodexPluginUsedMetadata `json:"event_params"`
}

// PluginUsedEventSink publishes the plugin-use analytics event. It is separate
// from PluginEventSink because the lifecycle sink does not have to report usage.
type PluginUsedEventSink interface {
	TrackCodexPluginUsedEvent(context.Context, CodexPluginUsedEventRequest)
}

func NewCodexPluginUsedEvent(params CodexPluginUsedMetadata) CodexPluginUsedEventRequest {
	return CodexPluginUsedEventRequest{EventType: CodexPluginUsedEventType, EventParams: params}
}

func (c *AnalyticsEventsClient) TrackCodexPluginUsedEvent(ctx context.Context, event CodexPluginUsedEventRequest) {
	c.trackEvent(event)
}

func (e *HTTPAnalyticsExporter) TrackCodexPluginUsedEvent(ctx context.Context, event CodexPluginUsedEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}
