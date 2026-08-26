package telemetry

import "context"

const CodexCommandExecutionEventType = "codex_command_execution_event"
const CodexFileChangeEventType = "codex_file_change_event"
const CodexMCPToolCallEventType = "codex_mcp_tool_call_event"
const CodexDynamicToolCallEventType = "codex_dynamic_tool_call_event"
const CodexCollabAgentToolCallEventType = "codex_collab_agent_tool_call_event"
const CodexWebSearchEventType = "codex_web_search_event"
const CodexImageGenerationEventType = "codex_image_generation_event"

const (
	ToolItemTerminalStatusCompleted   = "completed"
	ToolItemTerminalStatusFailed      = "failed"
	ToolItemTerminalStatusRejected    = "rejected"
	ToolItemTerminalStatusInterrupted = "interrupted"
)

const (
	ToolItemFailureKindToolError       = "tool_error"
	ToolItemFailureKindApprovalDenied  = "approval_denied"
	ToolItemFailureKindApprovalAborted = "approval_aborted"
	ToolItemFailureKindSandboxDenied   = "sandbox_denied"
	ToolItemFailureKindPolicyForbidden = "policy_forbidden"
)

const (
	FinalApprovalOutcomeUnknown                = "unknown"
	FinalApprovalOutcomeNotNeeded              = "not_needed"
	FinalApprovalOutcomeConfigAllowed          = "config_allowed"
	FinalApprovalOutcomePolicyForbidden        = "policy_forbidden"
	FinalApprovalOutcomeGuardianApproved       = "guardian_approved"
	FinalApprovalOutcomeGuardianDenied         = "guardian_denied"
	FinalApprovalOutcomeGuardianAborted        = "guardian_aborted"
	FinalApprovalOutcomeUserApproved           = "user_approved"
	FinalApprovalOutcomeUserApprovedForSession = "user_approved_for_session"
	FinalApprovalOutcomeUserDenied             = "user_denied"
	FinalApprovalOutcomeUserAborted            = "user_aborted"
)

type CommandExecutionEventSink interface {
	TrackCodexCommandExecutionEvent(context.Context, CodexCommandExecutionEventRequest)
}

type FileChangeEventSink interface {
	TrackCodexFileChangeEvent(context.Context, CodexFileChangeEventRequest)
}

type MCPToolCallEventSink interface {
	TrackCodexMCPToolCallEvent(context.Context, CodexMCPToolCallEventRequest)
}

type DynamicToolCallEventSink interface {
	TrackCodexDynamicToolCallEvent(context.Context, CodexDynamicToolCallEventRequest)
}

type CollabAgentToolCallEventSink interface {
	TrackCodexCollabAgentToolCallEvent(context.Context, CodexCollabAgentToolCallEventRequest)
}

type WebSearchEventSink interface {
	TrackCodexWebSearchEvent(context.Context, CodexWebSearchEventRequest)
}

type ImageGenerationEventSink interface {
	TrackCodexImageGenerationEvent(context.Context, CodexImageGenerationEventRequest)
}

type CodexToolItemEventBase struct {
	ThreadID                       string                       `json:"thread_id"`
	TurnID                         string                       `json:"turn_id"`
	ItemID                         string                       `json:"item_id"`
	AppServerClient                CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime                        CodexRuntimeMetadata         `json:"runtime"`
	ThreadSource                   *string                      `json:"thread_source"`
	SubagentSource                 *string                      `json:"subagent_source"`
	ParentThreadID                 *string                      `json:"parent_thread_id"`
	RootTurnID                     *string                      `json:"root_turn_id"`
	ToolName                       string                       `json:"tool_name"`
	StartedAtMS                    uint64                       `json:"started_at_ms"`
	CompletedAtMS                  uint64                       `json:"completed_at_ms"`
	DurationMS                     *uint64                      `json:"duration_ms"`
	ExecutionDurationMS            *uint64                      `json:"execution_duration_ms"`
	ReviewCount                    uint64                       `json:"review_count"`
	GuardianReviewCount            uint64                       `json:"guardian_review_count"`
	UserReviewCount                uint64                       `json:"user_review_count"`
	FinalApprovalOutcome           string                       `json:"final_approval_outcome"`
	TerminalStatus                 string                       `json:"terminal_status"`
	FailureKind                    *string                      `json:"failure_kind"`
	RequestedAdditionalPermissions bool                         `json:"requested_additional_permissions"`
	RequestedNetworkAccess         bool                         `json:"requested_network_access"`
}

type CodexCommandExecutionEventRequest struct {
	EventType   string                           `json:"event_type"`
	EventParams CodexCommandExecutionEventParams `json:"event_params"`
}

type CodexCommandExecutionEventParams struct {
	CodexToolItemEventBase
	PluginID                    *string `json:"plugin_id"`
	ScriptPath                  *string `json:"script_path"`
	CommandExecutionSource      string  `json:"command_execution_source"`
	ExitCode                    *int32  `json:"exit_code"`
	CommandTotalActionCount     uint64  `json:"command_total_action_count"`
	CommandReadActionCount      uint64  `json:"command_read_action_count"`
	CommandListFilesActionCount uint64  `json:"command_list_files_action_count"`
	CommandSearchActionCount    uint64  `json:"command_search_action_count"`
	CommandUnknownActionCount   uint64  `json:"command_unknown_action_count"`
}

type CodexFileChangeEventRequest struct {
	EventType   string                     `json:"event_type"`
	EventParams CodexFileChangeEventParams `json:"event_params"`
}

type CodexFileChangeEventParams struct {
	CodexToolItemEventBase
	FileChangeCount uint64 `json:"file_change_count"`
	FileAddCount    uint64 `json:"file_add_count"`
	FileUpdateCount uint64 `json:"file_update_count"`
	FileDeleteCount uint64 `json:"file_delete_count"`
	FileMoveCount   uint64 `json:"file_move_count"`
}

type CodexMCPToolCallEventRequest struct {
	EventType   string                      `json:"event_type"`
	EventParams CodexMCPToolCallEventParams `json:"event_params"`
}

type CodexMCPToolCallEventParams struct {
	CodexToolItemEventBase
	MCPServerName   string  `json:"mcp_server_name"`
	MCPToolName     string  `json:"mcp_tool_name"`
	MCPErrorPresent bool    `json:"mcp_error_present"`
	PluginID        *string `json:"plugin_id"`
}

type CodexDynamicToolCallEventRequest struct {
	EventType   string                          `json:"event_type"`
	EventParams CodexDynamicToolCallEventParams `json:"event_params"`
}

type CodexDynamicToolCallEventParams struct {
	CodexToolItemEventBase
	DynamicToolName        string  `json:"dynamic_tool_name"`
	Success                *bool   `json:"success"`
	OutputContentItemCount *uint64 `json:"output_content_item_count"`
	OutputTextItemCount    *uint64 `json:"output_text_item_count"`
	OutputImageItemCount   *uint64 `json:"output_image_item_count"`
}

type CodexCollabAgentToolCallEventRequest struct {
	EventType   string                              `json:"event_type"`
	EventParams CodexCollabAgentToolCallEventParams `json:"event_params"`
}

type CodexCollabAgentToolCallEventParams struct {
	CodexToolItemEventBase
	SenderThreadID           string   `json:"sender_thread_id"`
	ReceiverThreadCount      uint64   `json:"receiver_thread_count"`
	ReceiverThreadIDs        []string `json:"receiver_thread_ids"`
	RequestedModel           *string  `json:"requested_model"`
	RequestedReasoningEffort *string  `json:"requested_reasoning_effort"`
	AgentStateCount          *uint64  `json:"agent_state_count"`
	CompletedAgentCount      *uint64  `json:"completed_agent_count"`
	FailedAgentCount         *uint64  `json:"failed_agent_count"`
}

type CodexWebSearchEventRequest struct {
	EventType   string                    `json:"event_type"`
	EventParams CodexWebSearchEventParams `json:"event_params"`
}

type CodexWebSearchEventParams struct {
	CodexToolItemEventBase
	WebSearchAction *string `json:"web_search_action"`
	QueryPresent    bool    `json:"query_present"`
	QueryCount      *uint64 `json:"query_count"`
}

type CodexImageGenerationEventRequest struct {
	EventType   string                          `json:"event_type"`
	EventParams CodexImageGenerationEventParams `json:"event_params"`
}

type CodexImageGenerationEventParams struct {
	CodexToolItemEventBase
	RevisedPromptPresent bool `json:"revised_prompt_present"`
	SavedPathPresent     bool `json:"saved_path_present"`
	// TransparentBackground mirrors Rust #40544: the optional image-generation
	// transparent-background value is carried in the Analytics event params.
	TransparentBackground *bool `json:"transparent_background,omitempty"`
}

func NewCodexCommandExecutionEvent(params CodexCommandExecutionEventParams) CodexCommandExecutionEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	return CodexCommandExecutionEventRequest{
		EventType:   CodexCommandExecutionEventType,
		EventParams: params,
	}
}

func NewCodexFileChangeEvent(params CodexFileChangeEventParams) CodexFileChangeEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	return CodexFileChangeEventRequest{
		EventType:   CodexFileChangeEventType,
		EventParams: params,
	}
}

func NewCodexMCPToolCallEvent(params CodexMCPToolCallEventParams) CodexMCPToolCallEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	return CodexMCPToolCallEventRequest{
		EventType:   CodexMCPToolCallEventType,
		EventParams: params,
	}
}

func NewCodexDynamicToolCallEvent(params CodexDynamicToolCallEventParams) CodexDynamicToolCallEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	return CodexDynamicToolCallEventRequest{
		EventType:   CodexDynamicToolCallEventType,
		EventParams: params,
	}
}

func NewCodexCollabAgentToolCallEvent(params CodexCollabAgentToolCallEventParams) CodexCollabAgentToolCallEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	if params.ReceiverThreadIDs == nil {
		params.ReceiverThreadIDs = []string{}
	}
	return CodexCollabAgentToolCallEventRequest{
		EventType:   CodexCollabAgentToolCallEventType,
		EventParams: params,
	}
}

func NewCodexWebSearchEvent(params CodexWebSearchEventParams) CodexWebSearchEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	return CodexWebSearchEventRequest{
		EventType:   CodexWebSearchEventType,
		EventParams: params,
	}
}

func NewCodexImageGenerationEvent(params CodexImageGenerationEventParams) CodexImageGenerationEventRequest {
	if params.FinalApprovalOutcome == "" {
		params.FinalApprovalOutcome = FinalApprovalOutcomeUnknown
	}
	return CodexImageGenerationEventRequest{
		EventType:   CodexImageGenerationEventType,
		EventParams: params,
	}
}
