package telemetry

import (
	"context"
	"strings"
)

const CodexThreadInitializedEventType = "codex_thread_initialized"

type ThreadInitializedEventSink interface {
	TrackCodexThreadInitializedEvent(context.Context, CodexThreadInitializedEventRequest)
}

type CodexThreadInitializedEventRequest struct {
	EventType   string                            `json:"event_type"`
	EventParams CodexThreadInitializedEventParams `json:"event_params"`
}

type CodexThreadInitializedEventParams struct {
	ThreadID           string                       `json:"thread_id"`
	SessionID          string                       `json:"session_id"`
	AppServerClient    CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime            CodexRuntimeMetadata         `json:"runtime"`
	Model              string                       `json:"model"`
	Ephemeral          bool                         `json:"ephemeral"`
	ThreadSource       *string                      `json:"thread_source"`
	InitializationMode string                       `json:"initialization_mode"`
	SubagentSource     *string                      `json:"subagent_source"`
	ParentThreadID     *string                      `json:"parent_thread_id"`
	ForkedFromThreadID *string                      `json:"forked_from_thread_id"`
	CreatedAt          uint64                       `json:"created_at"`
}

type CodexThreadInitializedEventInput struct {
	ThreadID           string
	SessionID          string
	AppServerClient    CodexAppServerClientMetadata
	ThreadOriginator   string
	Runtime            CodexRuntimeMetadata
	Model              string
	Ephemeral          bool
	ThreadSource       *string
	InitializationMode string
	SubagentSource     *string
	ParentThreadID     *string
	ForkedFromThreadID *string
	CreatedAt          uint64
}

func NewCodexThreadInitializedEvent(input CodexThreadInitializedEventInput) CodexThreadInitializedEventRequest {
	client := input.AppServerClient
	if originator := strings.TrimSpace(input.ThreadOriginator); originator != "" {
		client.ProductClientID = originator
	}
	initializationMode := strings.TrimSpace(input.InitializationMode)
	if initializationMode == "" {
		initializationMode = "new"
	}
	return CodexThreadInitializedEventRequest{
		EventType: CodexThreadInitializedEventType,
		EventParams: CodexThreadInitializedEventParams{
			ThreadID:           input.ThreadID,
			SessionID:          input.SessionID,
			AppServerClient:    client,
			Runtime:            input.Runtime,
			Model:              input.Model,
			Ephemeral:          input.Ephemeral,
			ThreadSource:       input.ThreadSource,
			InitializationMode: initializationMode,
			SubagentSource:     input.SubagentSource,
			ParentThreadID:     input.ParentThreadID,
			ForkedFromThreadID: input.ForkedFromThreadID,
			CreatedAt:          input.CreatedAt,
		},
	}
}
