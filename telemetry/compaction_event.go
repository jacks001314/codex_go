package telemetry

import (
	"context"
	"strings"
)

const CodexCompactionEventType = "codex_compaction_event"

const (
	CompactionTriggerManual = "manual"
	CompactionTriggerAuto   = "auto"
)

const (
	CompactionReasonUserRequested   = "user_requested"
	CompactionReasonContextLimit    = "context_limit"
	CompactionReasonModelDownshift  = "model_downshift"
	CompactionReasonCompHashChanged = "comp_hash_changed"
)

const (
	CompactionImplementationResponses             = "responses"
	CompactionImplementationResponsesCompactionV2 = "responses_compaction_v2"
	CompactionImplementationResponsesCompact      = "responses_compact"
)

const (
	CompactionPhaseStandaloneTurn = "standalone_turn"
	CompactionPhasePreTurn        = "pre_turn"
	CompactionPhaseMidTurn        = "mid_turn"
)

const (
	CompactionStrategyMemento          = "memento"
	CompactionStrategyPrefixCompaction = "prefix_compaction"
)

const (
	CompactionStatusCompleted   = "completed"
	CompactionStatusFailed      = "failed"
	CompactionStatusInterrupted = "interrupted"
)

type CompactionEventSink interface {
	TrackCodexCompactionEvent(context.Context, CodexCompactionEventRequest)
}

type CodexCompactionEventRequest struct {
	EventType   string                     `json:"event_type"`
	EventParams CodexCompactionEventParams `json:"event_params"`
}

type CodexCompactionEventParams struct {
	ThreadID                        string                       `json:"thread_id"`
	SessionID                       string                       `json:"session_id"`
	TurnID                          string                       `json:"turn_id"`
	AppServerClient                 CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime                         CodexRuntimeMetadata         `json:"runtime"`
	ThreadSource                    *string                      `json:"thread_source"`
	SubagentSource                  *string                      `json:"subagent_source"`
	ParentThreadID                  *string                      `json:"parent_thread_id"`
	Trigger                         string                       `json:"trigger"`
	Reason                          string                       `json:"reason"`
	Implementation                  string                       `json:"implementation"`
	Phase                           string                       `json:"phase"`
	Strategy                        string                       `json:"strategy"`
	Status                          string                       `json:"status"`
	CodexErrorKind                  *string                      `json:"codex_error_kind"`
	CodexErrorHTTPStatusCode        *uint16                      `json:"codex_error_http_status_code"`
	ActiveContextTokensBefore       int64                        `json:"active_context_tokens_before"`
	ActiveContextTokensAfter        int64                        `json:"active_context_tokens_after"`
	RetainedImageCount              *int                         `json:"retained_image_count"`
	CompactionSummaryTokens         *int64                       `json:"compaction_summary_tokens"`
	CachedInputTokens               *int64                       `json:"cached_input_tokens"`
	CacheWriteInputTokens           *int64                       `json:"cache_write_input_tokens"`
	StartedAt                       uint64                       `json:"started_at"`
	CompletedAt                     uint64                       `json:"completed_at"`
	DurationMS                      *uint64                      `json:"duration_ms"`
	AutoCompactFallbackTriggered    bool                         `json:"auto_compact_fallback_triggered,omitempty"`
	AutoCompactFallbackOutcome      *string                      `json:"auto_compact_fallback_outcome,omitempty"`
	AutoCompactFallbackBufferTokens *int64                       `json:"auto_compact_fallback_buffer_tokens,omitempty"`
}

type CodexCompactionEventInput struct {
	ThreadID                        string
	SessionID                       string
	TurnID                          string
	AppServerClient                 CodexAppServerClientMetadata
	ThreadOriginator                string
	Runtime                         CodexRuntimeMetadata
	ThreadSource                    *string
	SubagentSource                  *string
	ParentThreadID                  *string
	Trigger                         string
	Reason                          string
	Implementation                  string
	Phase                           string
	Strategy                        string
	Status                          string
	CodexErrorKind                  *string
	CodexErrorHTTPStatusCode        *uint16
	ActiveContextTokensBefore       int64
	ActiveContextTokensAfter        int64
	RetainedImageCount              *int
	CompactionSummaryTokens         *int64
	CachedInputTokens               *int64
	CacheWriteInputTokens           *int64
	StartedAt                       uint64
	CompletedAt                     uint64
	DurationMS                      *uint64
	AutoCompactFallbackTriggered    bool
	AutoCompactFallbackOutcome      *string
	AutoCompactFallbackBufferTokens *int64
}

func NewCodexCompactionEvent(input CodexCompactionEventInput) CodexCompactionEventRequest {
	client := input.AppServerClient
	if originator := strings.TrimSpace(input.ThreadOriginator); originator != "" {
		client.ProductClientID = originator
	}
	return CodexCompactionEventRequest{
		EventType: CodexCompactionEventType,
		EventParams: CodexCompactionEventParams{
			ThreadID:                        input.ThreadID,
			SessionID:                       firstNonEmptyTelemetry(input.SessionID, input.ThreadID),
			TurnID:                          input.TurnID,
			AppServerClient:                 client,
			Runtime:                         input.Runtime,
			ThreadSource:                    input.ThreadSource,
			SubagentSource:                  input.SubagentSource,
			ParentThreadID:                  input.ParentThreadID,
			Trigger:                         firstNonEmptyTelemetry(input.Trigger, CompactionTriggerManual),
			Reason:                          firstNonEmptyTelemetry(input.Reason, CompactionReasonUserRequested),
			Implementation:                  firstNonEmptyTelemetry(input.Implementation, CompactionImplementationResponses),
			Phase:                           firstNonEmptyTelemetry(input.Phase, CompactionPhaseStandaloneTurn),
			Strategy:                        firstNonEmptyTelemetry(input.Strategy, CompactionStrategyMemento),
			Status:                          firstNonEmptyTelemetry(input.Status, CompactionStatusCompleted),
			CodexErrorKind:                  input.CodexErrorKind,
			CodexErrorHTTPStatusCode:        input.CodexErrorHTTPStatusCode,
			ActiveContextTokensBefore:       input.ActiveContextTokensBefore,
			ActiveContextTokensAfter:        input.ActiveContextTokensAfter,
			RetainedImageCount:              input.RetainedImageCount,
			CompactionSummaryTokens:         input.CompactionSummaryTokens,
			CachedInputTokens:               input.CachedInputTokens,
			CacheWriteInputTokens:           input.CacheWriteInputTokens,
			StartedAt:                       input.StartedAt,
			CompletedAt:                     input.CompletedAt,
			DurationMS:                      input.DurationMS,
			AutoCompactFallbackTriggered:    input.AutoCompactFallbackTriggered,
			AutoCompactFallbackOutcome:      input.AutoCompactFallbackOutcome,
			AutoCompactFallbackBufferTokens: input.AutoCompactFallbackBufferTokens,
		},
	}
}

func firstNonEmptyTelemetry(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
