package telemetry

import "context"

const CodexReviewEventType = "codex_review_event"

const (
	ReviewSubjectKindCommandExecution = "command_execution"
	ReviewSubjectKindFileChange       = "file_change"
	ReviewSubjectKindMCPToolCall      = "mcp_tool_call"
	ReviewSubjectKindPermissions      = "permissions"
	ReviewSubjectKindNetworkAccess    = "network_access"
)

const (
	ReviewerGuardian = "guardian"
	ReviewerUser     = "user"
)

const (
	ReviewTriggerInitial             = "initial"
	ReviewTriggerSandboxDenial       = "sandbox_denial"
	ReviewTriggerNetworkPolicyDenial = "network_policy_denial"
	ReviewTriggerExecveIntercept     = "execve_intercept"
)

const (
	ReviewStatusApproved = "approved"
	ReviewStatusDenied   = "denied"
	ReviewStatusAborted  = "aborted"
	ReviewStatusTimedOut = "timed_out"
)

const (
	ReviewResolutionNone                   = "none"
	ReviewResolutionSessionApproval        = "session_approval"
	ReviewResolutionExecPolicyAmendment    = "exec_policy_amendment"
	ReviewResolutionNetworkPolicyAmendment = "network_policy_amendment"
)

type ReviewEventSink interface {
	TrackCodexReviewEvent(context.Context, CodexReviewEventRequest)
}

type CodexReviewEventRequest struct {
	EventType   string                 `json:"event_type"`
	EventParams CodexReviewEventParams `json:"event_params"`
}

type CodexReviewEventParams struct {
	ThreadID        string                       `json:"thread_id"`
	TurnID          string                       `json:"turn_id"`
	ItemID          *string                      `json:"item_id"`
	ReviewID        string                       `json:"review_id"`
	AppServerClient CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime         CodexRuntimeMetadata         `json:"runtime"`
	ThreadSource    *string                      `json:"thread_source"`
	SubagentSource  *string                      `json:"subagent_source"`
	ParentThreadID  *string                      `json:"parent_thread_id"`
	SubjectKind     string                       `json:"subject_kind"`
	SubjectName     string                       `json:"subject_name"`
	Reviewer        string                       `json:"reviewer"`
	Trigger         string                       `json:"trigger"`
	Status          string                       `json:"status"`
	Resolution      string                       `json:"resolution"`
	StartedAtMS     uint64                       `json:"started_at_ms"`
	CompletedAtMS   uint64                       `json:"completed_at_ms"`
	DurationMS      *uint64                      `json:"duration_ms"`
}

func NewCodexReviewEvent(params CodexReviewEventParams) CodexReviewEventRequest {
	return CodexReviewEventRequest{
		EventType:   CodexReviewEventType,
		EventParams: params,
	}
}
