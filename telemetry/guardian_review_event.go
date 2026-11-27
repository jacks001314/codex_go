package telemetry

import (
	"context"
	"encoding/json"
)

// CodexGuardianReviewEventType is the analytics event emitted by the
// synchronous Guardian reviewer.
const CodexGuardianReviewEventType = "codex_guardian_review"

// GuardianReviewEventSink publishes terminal Guardian review analytics.
type GuardianReviewEventSink interface {
	TrackCodexGuardianReviewEvent(context.Context, GuardianReviewEventRequest)
}

// GuardianReviewEventRequest mirrors Rust's GuardianReviewEventRequest. The
// review fields are flattened into event_params alongside host attribution.
type GuardianReviewEventRequest struct {
	EventType   string                    `json:"event_type"`
	EventParams GuardianReviewEventParams `json:"event_params"`
}

// GuardianReviewEventParams mirrors Rust's GuardianReviewEventPayload with
// GuardianReviewEventParams flattened. Optional Rust fields serialize as null;
// keeping them in the payload prevents consumers from treating an unknown value
// as an omitted field.
type GuardianReviewEventParams struct {
	SessionID       string                       `json:"session_id"`
	AppServerClient CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime         CodexRuntimeMetadata         `json:"runtime"`

	GuardianContextMode     *string         `json:"guardian_context_mode"`
	ThreadID                string          `json:"thread_id"`
	TurnID                  string          `json:"turn_id"`
	RootTurnID              *string         `json:"root_turn_id"`
	ReviewID                string          `json:"review_id"`
	TargetItemID            *string         `json:"target_item_id"`
	ApprovalRequestSource   string          `json:"approval_request_source"`
	ReviewedAction          json.RawMessage `json:"reviewed_action"`
	ReviewedActionTruncated bool            `json:"reviewed_action_truncated"`
	Decision                string          `json:"decision"`
	TerminalStatus          string          `json:"terminal_status"`
	FailureReason           *string         `json:"failure_reason"`
	AttemptCount            int64           `json:"attempt_count"`
	RiskLevel               *string         `json:"risk_level"`
	UserAuthorization       *string         `json:"user_authorization"`
	Outcome                 *string         `json:"outcome"`
	GuardianThreadID        *string         `json:"guardian_thread_id"`
	GuardianSessionKind     *string         `json:"guardian_session_kind"`
	GuardianModel           *string         `json:"guardian_model"`
	GuardianReasoningEffort *string         `json:"guardian_reasoning_effort"`
	GuardianDefaultModelID  *string         `json:"guardian_default_review_model_id"`
	GuardianCatalogHasAuto  *bool           `json:"guardian_catalog_contains_auto_review"`
	GuardianModelOverridden *bool           `json:"guardian_review_model_overridden"`
	GuardianModelOverride   *string         `json:"guardian_review_model_override"`
	GuardianModelProviderID *string         `json:"guardian_model_provider_id"`
	HadPriorReviewContext   *bool           `json:"had_prior_review_context"`
	ReviewTimeoutMS         uint64          `json:"review_timeout_ms"`
	ToolCallCount           *uint64         `json:"tool_call_count"`
	TimeToFirstTokenMS      *uint64         `json:"time_to_first_token_ms"`
	CompletionLatencyMS     *uint64         `json:"completion_latency_ms"`
	StartedAt               uint64          `json:"started_at"`
	CompletedAt             *uint64         `json:"completed_at"`
	InputTokens             *int64          `json:"input_tokens"`
	CachedInputTokens       *int64          `json:"cached_input_tokens"`
	CacheWriteInputTokens   *int64          `json:"cache_write_input_tokens"`
	OutputTokens            *int64          `json:"output_tokens"`
	ReasoningOutputTokens   *int64          `json:"reasoning_output_tokens"`
	TotalTokens             *int64          `json:"total_tokens"`
}

// NewGuardianReviewEvent builds a codex_guardian_review analytics request.
func NewGuardianReviewEvent(params GuardianReviewEventParams) GuardianReviewEventRequest {
	return GuardianReviewEventRequest{
		EventType:   CodexGuardianReviewEventType,
		EventParams: params,
	}
}
