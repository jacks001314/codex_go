package telemetry

import "context"

const CodexHookRunEventType = "codex_hook_run"

type HookRunEventSink interface {
	TrackCodexHookRunEvent(context.Context, CodexHookRunEventRequest)
}

type CodexHookRunEventRequest struct {
	EventType   string                 `json:"event_type"`
	EventParams CodexHookRunMetadataV1 `json:"event_params"`
}

type CodexHookRunMetadataV1 struct {
	ThreadID        *string `json:"thread_id"`
	TurnID          *string `json:"turn_id"`
	ProductClientID *string `json:"product_client_id"`
	ModelSlug       *string `json:"model_slug"`
	HookName        *string `json:"hook_name"`
	HookSource      *string `json:"hook_source"`
	Status          *string `json:"status"`
}

func NewCodexHookRunEvent(metadata CodexHookRunMetadataV1) CodexHookRunEventRequest {
	return CodexHookRunEventRequest{
		EventType:   CodexHookRunEventType,
		EventParams: metadata,
	}
}
