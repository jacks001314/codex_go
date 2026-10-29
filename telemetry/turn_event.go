package telemetry

import (
	"context"
	"runtime"
	"strings"
)

const CodexTurnEventType = "codex_turn_event"

type TurnEventSink interface {
	TrackCodexTurnEvent(context.Context, CodexTurnEventRequest)
}

type TurnEventSinkFunc func(context.Context, CodexTurnEventRequest)

func (f TurnEventSinkFunc) TrackCodexTurnEvent(ctx context.Context, event CodexTurnEventRequest) {
	if f != nil {
		f(ctx, event)
	}
}

type AppServerRPCTransport string

const (
	AppServerRPCTransportStdio     AppServerRPCTransport = "stdio"
	AppServerRPCTransportWebsocket AppServerRPCTransport = "websocket"
	AppServerRPCTransportInProcess AppServerRPCTransport = "in_process"
)

type CodexAppServerClientMetadata struct {
	ProductClientID       string                `json:"product_client_id"`
	ClientName            *string               `json:"client_name"`
	ClientVersion         *string               `json:"client_version"`
	RPCTransport          AppServerRPCTransport `json:"rpc_transport"`
	ExperimentalAPIEnable *bool                 `json:"experimental_api_enabled"`
}

type CodexRuntimeMetadata struct {
	CodexRSVersion   string `json:"codex_rs_version"`
	RuntimeOS        string `json:"runtime_os"`
	RuntimeOSVersion string `json:"runtime_os_version"`
	RuntimeArch      string `json:"runtime_arch"`
}

func CurrentRuntimeMetadata() CodexRuntimeMetadata {
	return CodexRuntimeMetadata{
		CodexRSVersion:   "0.0.0",
		RuntimeOS:        runtime.GOOS,
		RuntimeOSVersion: runtime.GOOS,
		RuntimeArch:      runtime.GOARCH,
	}
}

type CodexTurnEventRequest struct {
	EventType   string               `json:"event_type"`
	EventParams CodexTurnEventParams `json:"event_params"`
}

// CodexTurnCriticalPathStatus mirrors Rust's TurnCriticalPath capture status
// (`disabled`, `incomplete`, or `complete`; Rust #57209).
const (
	CodexTurnCriticalPathStatusDisabled   = "disabled"
	CodexTurnCriticalPathStatusIncomplete = "incomplete"
	CodexTurnCriticalPathStatusComplete   = "complete"
)

type CodexTurnEventParams struct {
	// Critical path captures are only reported as complete when the turn's
	// default-off `critical_path_timing` feature produced a frozen dependency
	// path (Rust #57201/#57209). Missing capture never becomes a zero
	// measurement.
	CriticalPathStatus      string  `json:"critical_path_status"`
	CriticalPathDurationMS  *uint64 `json:"critical_path_duration_ms"`
	CriticalPathInferenceMS *uint64 `json:"critical_path_inference_ms"`
	CriticalPathAutoReview  *uint64 `json:"critical_path_auto_review_ms"`
	CriticalPathUserWaitMS  *uint64 `json:"critical_path_user_wait_ms"`
	CriticalPathToolsMS     *uint64 `json:"critical_path_tools_ms"`
	CriticalPathOverheadMS  *uint64 `json:"critical_path_overhead_ms"`
	CriticalPathUnknownMS   *uint64 `json:"critical_path_unknown_ms"`
	ThreadID                string  `json:"thread_id"`
	SessionID               string  `json:"session_id"`
	TurnID                  string  `json:"turn_id"`
	// VoiceSessionID attributes this turn to a realtime voice session when the
	// turn was started or steered by one (Rust #46058).
	VoiceSessionID *string `json:"voice_session_id"`
	// RootTurnID is the top-level turn that caused this activity, or null when
	// the root is unknown or ambiguous (Rust #40486).
	RootTurnID *string `json:"root_turn_id"`
	// ActivePluginIDsAtTurnStart is the first observed active plugin inventory:
	// nil is unknown (JSON null) and an empty slice is an observed empty
	// inventory (JSON []), matching Rust #46323.
	ActivePluginIDsAtTurnStart           *[]string                    `json:"active_plugin_ids_at_turn_start"`
	TurnTrigger                          *string                      `json:"turn_trigger"`
	CodexTurnSource                      *string                      `json:"codex_turn_source"`
	SubmissionType                       *string                      `json:"submission_type"`
	AppServerClient                      CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime                              CodexRuntimeMetadata         `json:"runtime"`
	Ephemeral                            bool                         `json:"ephemeral"`
	ThreadSource                         *string                      `json:"thread_source"`
	InitializationMode                   string                       `json:"initialization_mode"`
	SubagentSource                       *string                      `json:"subagent_source"`
	ParentThreadID                       *string                      `json:"parent_thread_id"`
	Model                                *string                      `json:"model"`
	ModelProvider                        string                       `json:"model_provider"`
	SandboxPolicy                        *string                      `json:"sandbox_policy"`
	ReasoningEffort                      *string                      `json:"reasoning_effort"`
	ReasoningSummary                     *string                      `json:"reasoning_summary"`
	ServiceTier                          string                       `json:"service_tier"`
	ApprovalPolicy                       string                       `json:"approval_policy"`
	ApprovalsReviewer                    string                       `json:"approvals_reviewer"`
	GuardianV2Enabled                    bool                         `json:"guardian_v2_enabled"`
	SandboxNetworkAccess                 bool                         `json:"sandbox_network_access"`
	CollaborationMode                    *string                      `json:"collaboration_mode"`
	Personality                          *string                      `json:"personality"`
	WorkspaceKind                        *string                      `json:"workspace_kind"`
	NumInputImages                       int                          `json:"num_input_images"`
	IsFirstTurn                          bool                         `json:"is_first_turn"`
	Status                               *string                      `json:"status"`
	ExplicitClientInterruptRequestedAtMS *uint64                      `json:"explicit_client_interrupt_requested_at_ms"`
	TurnError                            any                          `json:"turn_error"`
	CodexErrorKind                       *string                      `json:"codex_error_kind"`
	CodexErrorHTTPStatusCode             *uint16                      `json:"codex_error_http_status_code"`
	UsageLimitWindowMinutes              *uint16                      `json:"usage_limit_window_minutes"`
	SteerCount                           *int                         `json:"steer_count"`
	RunningBackgroundProcessCount        *int                         `json:"running_background_process_count"`
	TotalToolCallCount                   *int                         `json:"total_tool_call_count"`
	ShellCommandCount                    *int                         `json:"shell_command_count"`
	FileChangeCount                      *int                         `json:"file_change_count"`
	MCPToolCallCount                     *int                         `json:"mcp_tool_call_count"`
	DynamicToolCallCount                 *int                         `json:"dynamic_tool_call_count"`
	SubagentToolCallCount                *int                         `json:"subagent_tool_call_count"`
	WebSearchCount                       *int                         `json:"web_search_count"`
	ImageGenerationCount                 *int                         `json:"image_generation_count"`
	InputTokens                          *int64                       `json:"input_tokens"`
	CachedInputTokens                    *int64                       `json:"cached_input_tokens"`
	CacheWriteInputTokens                *int64                       `json:"cache_write_input_tokens"`
	OutputTokens                         *int64                       `json:"output_tokens"`
	ReasoningOutputTokens                *int64                       `json:"reasoning_output_tokens"`
	TotalTokens                          *int64                       `json:"total_tokens"`
	BeforeFirstSamplingMS                uint64                       `json:"before_first_sampling_ms"`
	SamplingMS                           uint64                       `json:"sampling_ms"`
	BetweenSamplingOverheadMS            uint64                       `json:"between_sampling_overhead_ms"`
	ToolBlockingMS                       uint64                       `json:"tool_blocking_ms"`
	AfterLastSamplingMS                  uint64                       `json:"after_last_sampling_ms"`
	SamplingRequestCount                 uint32                       `json:"sampling_request_count"`
	SamplingRetryCount                   uint32                       `json:"sampling_retry_count"`
	// ToolsChangeCount is the number of sampling requests in the turn whose full
	// model-visible tool list differed from the previous request's list
	// (Rust #50964 `CodexTurnEventParams::tools_change_count`).
	ToolsChangeCount uint32  `json:"tools_change_count"`
	DurationMS       *uint64 `json:"duration_ms"`
	StartedAt        *uint64 `json:"started_at"`
	CompletedAt      *uint64 `json:"completed_at"`
	// MultiAgentVersion is the turn's resolved multi-agent version, serialized
	// like Rust's protocol::MultiAgentVersion: disabled | v1 | v2 (Rust #51333).
	MultiAgentVersion string `json:"multi_agent_version"`
}

type CodexTurnEventInput struct {
	ThreadID                             string
	SessionID                            string
	TurnID                               string
	VoiceSessionID                       *string
	RootTurnID                           *string
	ActivePluginIDsAtTurnStart           *[]string
	TurnTrigger                          string
	CodexTurnSource                      string
	SubmissionType                       *string
	AppServerClient                      CodexAppServerClientMetadata
	ThreadOriginator                     string
	Runtime                              CodexRuntimeMetadata
	Ephemeral                            bool
	ThreadSource                         *string
	InitializationMode                   string
	SubagentSource                       *string
	ParentThreadID                       *string
	Model                                *string
	ModelProvider                        string
	SandboxPolicy                        *string
	ReasoningEffort                      *string
	ReasoningSummary                     *string
	ServiceTier                          string
	ApprovalPolicy                       string
	ApprovalsReviewer                    string
	GuardianV2Enabled                    bool
	SandboxNetworkAccess                 bool
	CollaborationMode                    *string
	Personality                          *string
	WorkspaceKind                        *string
	NumInputImages                       int
	IsFirstTurn                          bool
	Status                               *string
	ExplicitClientInterruptRequestedAtMS *uint64
	TurnError                            any
	CodexErrorKind                       *string
	CodexErrorHTTPStatusCode             *uint16
	UsageLimitWindowMinutes              *uint16
	SteerCount                           *int
	RunningBackgroundProcessCount        *int
	ToolCounts                           *CodexTurnToolCounts
	TokenUsage                           *CodexTurnTokenUsage
	TimingProfile                        CodexTurnTimingProfile
	DurationMS                           *uint64
	StartedAt                            *uint64
	CompletedAt                          *uint64
	// MultiAgentVersion is the turn's resolved multi-agent version; empty means
	// the multi-agent surface is disabled (Rust's MultiAgentVersion::Disabled).
	MultiAgentVersion string
}

type CodexTurnToolCounts struct {
	Total            int
	ShellCommand     int
	FileChange       int
	MCPToolCall      int
	DynamicToolCall  int
	SubagentToolCall int
	WebSearch        int
	ImageGeneration  int
}

type CodexTurnTokenUsage struct {
	InputTokens           int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64
}

type CodexTurnTimingProfile struct {
	BeforeFirstSamplingMS     uint64
	SamplingMS                uint64
	BetweenSamplingOverheadMS uint64
	ToolBlockingMS            uint64
	AfterLastSamplingMS       uint64
	SamplingRequestCount      uint32
	SamplingRetryCount        uint32
	// ToolsChangeCount mirrors Rust's `TurnProfile::tools_change_count` (#50964).
	ToolsChangeCount uint32
	// CriticalPath is the turn's capture coverage (Rust #57201). Its zero value
	// reports disabled.
	CriticalPath CodexTurnCriticalPath
}

// CodexTurnCriticalPath is the finalized critical-path capture for one turn.
// Missing capture is never a zero-duration measurement.
type CodexTurnCriticalPath struct {
	Status    string
	Durations *CodexTurnCriticalPathDurations
}

// CodexTurnCriticalPathDurations holds exclusive category totals over the turn
// interval.
type CodexTurnCriticalPathDurations struct {
	DurationMS   uint64
	InferenceMS  uint64
	AutoReviewMS uint64
	UserWaitMS   uint64
	ToolsMS      uint64
	OverheadMS   uint64
	UnknownMS    uint64
}

// multiAgentVersionValue maps the resolved turn version onto the value the event
// carries; a turn without the multi-agent surface reports Rust's Disabled arm
// (Rust #51333).
func multiAgentVersionValue(version string) string {
	if value := strings.TrimSpace(version); value != "" {
		return value
	}
	return MultiAgentVersionDisabled
}

func NewCodexTurnEvent(input CodexTurnEventInput) CodexTurnEventRequest {
	client := input.AppServerClient
	if originator := strings.TrimSpace(input.ThreadOriginator); originator != "" {
		client.ProductClientID = originator
	}
	serviceTier := strings.TrimSpace(input.ServiceTier)
	if serviceTier == "" {
		serviceTier = "default"
	}
	initializationMode := strings.TrimSpace(input.InitializationMode)
	if initializationMode == "" {
		initializationMode = "new"
	}
	return CodexTurnEventRequest{
		EventType: CodexTurnEventType,
		EventParams: CodexTurnEventParams{
			ThreadID:                             input.ThreadID,
			SessionID:                            input.SessionID,
			TurnID:                               input.TurnID,
			VoiceSessionID:                       input.VoiceSessionID,
			RootTurnID:                           input.RootTurnID,
			ActivePluginIDsAtTurnStart:           input.ActivePluginIDsAtTurnStart,
			TurnTrigger:                          boundedTelemetryString(input.TurnTrigger),
			CodexTurnSource:                      boundedTelemetryString(input.CodexTurnSource),
			SubmissionType:                       input.SubmissionType,
			AppServerClient:                      client,
			Runtime:                              input.Runtime,
			Ephemeral:                            input.Ephemeral,
			ThreadSource:                         input.ThreadSource,
			InitializationMode:                   initializationMode,
			SubagentSource:                       input.SubagentSource,
			ParentThreadID:                       input.ParentThreadID,
			Model:                                input.Model,
			ModelProvider:                        input.ModelProvider,
			SandboxPolicy:                        input.SandboxPolicy,
			ReasoningEffort:                      input.ReasoningEffort,
			ReasoningSummary:                     input.ReasoningSummary,
			ServiceTier:                          serviceTier,
			ApprovalPolicy:                       input.ApprovalPolicy,
			ApprovalsReviewer:                    input.ApprovalsReviewer,
			GuardianV2Enabled:                    input.GuardianV2Enabled,
			MultiAgentVersion:                    multiAgentVersionValue(input.MultiAgentVersion),
			SandboxNetworkAccess:                 input.SandboxNetworkAccess,
			CollaborationMode:                    input.CollaborationMode,
			Personality:                          input.Personality,
			WorkspaceKind:                        input.WorkspaceKind,
			NumInputImages:                       input.NumInputImages,
			IsFirstTurn:                          input.IsFirstTurn,
			Status:                               input.Status,
			ExplicitClientInterruptRequestedAtMS: input.ExplicitClientInterruptRequestedAtMS,
			TurnError:                            input.TurnError,
			CodexErrorKind:                       input.CodexErrorKind,
			CodexErrorHTTPStatusCode:             input.CodexErrorHTTPStatusCode,
			UsageLimitWindowMinutes:              input.UsageLimitWindowMinutes,
			SteerCount:                           input.SteerCount,
			RunningBackgroundProcessCount:        input.RunningBackgroundProcessCount,
			TotalToolCallCount:                   toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.Total }),
			ShellCommandCount:                    toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.ShellCommand }),
			FileChangeCount:                      toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.FileChange }),
			MCPToolCallCount:                     toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.MCPToolCall }),
			DynamicToolCallCount:                 toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.DynamicToolCall }),
			SubagentToolCallCount:                toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.SubagentToolCall }),
			WebSearchCount:                       toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.WebSearch }),
			ImageGenerationCount:                 toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.ImageGeneration }),
			InputTokens:                          tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.InputTokens }),
			CachedInputTokens:                    tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.CachedInputTokens }),
			CacheWriteInputTokens:                tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.CacheWriteInputTokens }),
			OutputTokens:                         tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.OutputTokens }),
			ReasoningOutputTokens:                tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.ReasoningOutputTokens }),
			TotalTokens:                          tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.TotalTokens }),
			BeforeFirstSamplingMS:                input.TimingProfile.BeforeFirstSamplingMS,
			SamplingMS:                           input.TimingProfile.SamplingMS,
			BetweenSamplingOverheadMS:            input.TimingProfile.BetweenSamplingOverheadMS,
			ToolBlockingMS:                       input.TimingProfile.ToolBlockingMS,
			AfterLastSamplingMS:                  input.TimingProfile.AfterLastSamplingMS,
			SamplingRequestCount:                 input.TimingProfile.SamplingRequestCount,
			SamplingRetryCount:                   input.TimingProfile.SamplingRetryCount,
			ToolsChangeCount:                     input.TimingProfile.ToolsChangeCount,
			CriticalPathStatus:                   criticalPathStatus(input.TimingProfile.CriticalPath),
			CriticalPathDurationMS:               criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.DurationMS }),
			CriticalPathInferenceMS:              criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.InferenceMS }),
			CriticalPathAutoReview:               criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.AutoReviewMS }),
			CriticalPathUserWaitMS:               criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.UserWaitMS }),
			CriticalPathToolsMS:                  criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.ToolsMS }),
			CriticalPathOverheadMS:               criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.OverheadMS }),
			CriticalPathUnknownMS:                criticalPathDuration(input.TimingProfile.CriticalPath, func(value CodexTurnCriticalPathDurations) uint64 { return value.UnknownMS }),
			DurationMS:                           input.DurationMS,
			StartedAt:                            input.StartedAt,
			CompletedAt:                          input.CompletedAt,
		},
	}
}

func criticalPathStatus(path CodexTurnCriticalPath) string {
	switch path.Status {
	case CodexTurnCriticalPathStatusIncomplete:
		return CodexTurnCriticalPathStatusIncomplete
	case CodexTurnCriticalPathStatusComplete:
		return CodexTurnCriticalPathStatusComplete
	default:
		return CodexTurnCriticalPathStatusDisabled
	}
}

func criticalPathDuration(path CodexTurnCriticalPath, value func(CodexTurnCriticalPathDurations) uint64) *uint64 {
	if criticalPathStatus(path) != CodexTurnCriticalPathStatusComplete || path.Durations == nil {
		return nil
	}
	duration := value(*path.Durations)
	return &duration
}

func boundedTelemetryString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return nil
	}
	return &value
}

func toolCountPtr(counts *CodexTurnToolCounts, value func(CodexTurnToolCounts) int) *int {
	if counts == nil {
		return nil
	}
	out := value(*counts)
	return &out
}

func tokenUsagePtr(usage *CodexTurnTokenUsage, value func(CodexTurnTokenUsage) int64) *int64 {
	if usage == nil {
		return nil
	}
	out := value(*usage)
	return &out
}
