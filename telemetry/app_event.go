package telemetry

import (
	"context"
	"log/slog"
)

const (
	CodexAppMentionedEventType = "codex_app_mentioned"
	CodexAppUsedEventType      = "codex_app_used"
)

// App invocation classes (Rust InvocationType): a connector the user's input (or
// a skill) named explicitly, or one the model picked on its own.
const (
	InvocationTypeExplicit = "explicit"
	InvocationTypeImplicit = "implicit"
)

// CodexAppMetadata is the identity a codex_app_* event reports (Rust's
// CodexAppMetadata).
type CodexAppMetadata struct {
	ConnectorID     *string `json:"connector_id"`
	ThreadID        *string `json:"thread_id"`
	TurnID          *string `json:"turn_id"`
	AppName         *string `json:"app_name"`
	ProductClientID *string `json:"product_client_id"`
	InvokeType      *string `json:"invoke_type"`
	ModelSlug       *string `json:"model_slug"`
}

// CodexAppMentionedEventRequest reports an app the input named explicitly.
type CodexAppMentionedEventRequest struct {
	EventType   string           `json:"event_type"`
	EventParams CodexAppMetadata `json:"event_params"`
}

// CodexAppUsedEventParams is the mentioned metadata plus the turn's voice session
// and the elicitation classification the call carried (Rust's
// CodexAppUsedMetadata).
type CodexAppUsedEventParams struct {
	CodexAppMetadata
	VoiceSessionID  *string `json:"voice_session_id"`
	ElicitationType *string `json:"elicitation_type"`
}

// CodexAppUsedEventRequest reports one app invocation.
type CodexAppUsedEventRequest struct {
	EventType   string                  `json:"event_type"`
	EventParams CodexAppUsedEventParams `json:"event_params"`
}

// AppEventSink publishes the app analytics events.
type AppEventSink interface {
	TrackCodexAppMentionedEvent(context.Context, CodexAppMentionedEventRequest)
	TrackCodexAppUsedEvent(context.Context, CodexAppUsedEventRequest)
}

func NewCodexAppMentionedEvent(params CodexAppMetadata) CodexAppMentionedEventRequest {
	return CodexAppMentionedEventRequest{EventType: CodexAppMentionedEventType, EventParams: params}
}

func NewCodexAppUsedEvent(params CodexAppUsedEventParams) CodexAppUsedEventRequest {
	return CodexAppUsedEventRequest{EventType: CodexAppUsedEventType, EventParams: params}
}

func (c *AnalyticsEventsClient) TrackCodexAppMentionedEvent(ctx context.Context, event CodexAppMentionedEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexAppUsedEvent(ctx context.Context, event CodexAppUsedEventRequest) {
	c.trackEvent(event)
}

func (e *HTTPAnalyticsExporter) TrackCodexAppMentionedEvent(ctx context.Context, event CodexAppMentionedEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexAppUsedEvent(ctx context.Context, event CodexAppUsedEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}
