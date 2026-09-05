package telemetry

import "context"

const (
	CodexGuardianV2ClassificationEventType = "codex_guardian_v2_classification"
	CodexGuardianV2FastDecisionEventType   = "codex_guardian_v2_fast_decision"
)

type GuardianV2EventSink interface {
	TrackCodexGuardianV2Event(context.Context, GuardianV2EventRequest)
}

type GuardianV2EventSinkFunc func(context.Context, GuardianV2EventRequest)

func (f GuardianV2EventSinkFunc) TrackCodexGuardianV2Event(ctx context.Context, event GuardianV2EventRequest) {
	if f != nil {
		f(ctx, event)
	}
}

// GuardianV2EventRequest mirrors Rust's analytics GuardianV2EventRequest.
type GuardianV2EventRequest struct {
	EventType   string                `json:"event_type"`
	EventParams GuardianV2EventParams `json:"event_params"`
}

// GuardianV2EventParams mirrors Rust's flattened GuardianV2EventParams plus
// the GuardianV2Event attribution.
type GuardianV2EventParams struct {
	SessionID       string                       `json:"session_id"`
	AppServerClient CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime         CodexRuntimeMetadata         `json:"runtime"`
	ThreadSource    *string                      `json:"thread_source"`
	SubagentSource  *string                      `json:"subagent_source"`
	ParentThreadID  *string                      `json:"parent_thread_id"`
	GuardianV2Event
}

// GuardianV2Event carries bounded attribution and one kind of event outcome.
type GuardianV2Event struct {
	ThreadID     string  `json:"thread_id"`
	TurnID       string  `json:"turn_id"`
	ItemID       *string `json:"item_id"`
	Model        *string `json:"model"`
	OccurredAtMS uint64  `json:"occurred_at_ms"`
	Outcome      string  `json:"outcome,omitempty"`
	RiskLevel    *string `json:"risk_level,omitempty"`
	DurationMS   uint64  `json:"duration_ms,omitempty"`
	Decision     string  `json:"decision,omitempty"`
}

// NewGuardianV2ClassificationEvent builds a classification event request.
func NewGuardianV2ClassificationEvent(params GuardianV2EventParams) GuardianV2EventRequest {
	return GuardianV2EventRequest{EventType: CodexGuardianV2ClassificationEventType, EventParams: params}
}

// NewGuardianV2FastDecisionEvent builds a fast-decision event request.
func NewGuardianV2FastDecisionEvent(params GuardianV2EventParams) GuardianV2EventRequest {
	return GuardianV2EventRequest{EventType: CodexGuardianV2FastDecisionEventType, EventParams: params}
}
