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

type CodexTurnEventParams struct {
	ThreadID                  string                       `json:"thread_id"`
	SessionID                 string                       `json:"session_id"`
	TurnID                    string                       `json:"turn_id"`
	SubmissionType            *string                      `json:"submission_type"`
	AppServerClient           CodexAppServerClientMetadata `json:"app_server_client"`
	Runtime                   CodexRuntimeMetadata         `json:"runtime"`
	Ephemeral                 bool                         `json:"ephemeral"`
	ThreadSource              *string                      `json:"thread_source"`
	InitializationMode        string                       `json:"initialization_mode"`
	SubagentSource            *string                      `json:"subagent_source"`
	ParentThreadID            *string                      `json:"parent_thread_id"`
	Model                     *string                      `json:"model"`
	ModelProvider             string                       `json:"model_provider"`
	SandboxPolicy             *string                      `json:"sandbox_policy"`
	ReasoningEffort           *string                      `json:"reasoning_effort"`
	ReasoningSummary          *string                      `json:"reasoning_summary"`
	ServiceTier               string                       `json:"service_tier"`
	ApprovalPolicy            string                       `json:"approval_policy"`
	ApprovalsReviewer         string                       `json:"approvals_reviewer"`
	SandboxNetworkAccess      bool                         `json:"sandbox_network_access"`
	CollaborationMode         *string                      `json:"collaboration_mode"`
	Personality               *string                      `json:"personality"`
	WorkspaceKind             *string                      `json:"workspace_kind"`
	NumInputImages            int                          `json:"num_input_images"`
	IsFirstTurn               bool                         `json:"is_first_turn"`
	Status                    *string                      `json:"status"`
	TurnError                 any                          `json:"turn_error"`
	CodexErrorKind            *string                      `json:"codex_error_kind"`
	CodexErrorHTTPStatusCode  *uint16                      `json:"codex_error_http_status_code"`
	SteerCount                *int                         `json:"steer_count"`
	TotalToolCallCount        *int                         `json:"total_tool_call_count"`
	ShellCommandCount         *int                         `json:"shell_command_count"`
	FileChangeCount           *int                         `json:"file_change_count"`
	MCPToolCallCount          *int                         `json:"mcp_tool_call_count"`
	DynamicToolCallCount      *int                         `json:"dynamic_tool_call_count"`
	SubagentToolCallCount     *int                         `json:"subagent_tool_call_count"`
	WebSearchCount            *int                         `json:"web_search_count"`
	ImageGenerationCount      *int                         `json:"image_generation_count"`
	InputTokens               *int64                       `json:"input_tokens"`
	CachedInputTokens         *int64                       `json:"cached_input_tokens"`
	OutputTokens              *int64                       `json:"output_tokens"`
	ReasoningOutputTokens     *int64                       `json:"reasoning_output_tokens"`
	TotalTokens               *int64                       `json:"total_tokens"`
	BeforeFirstSamplingMS     uint64                       `json:"before_first_sampling_ms"`
	SamplingMS                uint64                       `json:"sampling_ms"`
	BetweenSamplingOverheadMS uint64                       `json:"between_sampling_overhead_ms"`
	ToolBlockingMS            uint64                       `json:"tool_blocking_ms"`
	AfterLastSamplingMS       uint64                       `json:"after_last_sampling_ms"`
	SamplingRequestCount      uint32                       `json:"sampling_request_count"`
	SamplingRetryCount        uint32                       `json:"sampling_retry_count"`
	DurationMS                *uint64                      `json:"duration_ms"`
	StartedAt                 *uint64                      `json:"started_at"`
	CompletedAt               *uint64                      `json:"completed_at"`
}

type CodexTurnEventInput struct {
	ThreadID                 string
	SessionID                string
	TurnID                   string
	SubmissionType           *string
	AppServerClient          CodexAppServerClientMetadata
	ThreadOriginator         string
	Runtime                  CodexRuntimeMetadata
	Ephemeral                bool
	ThreadSource             *string
	InitializationMode       string
	SubagentSource           *string
	ParentThreadID           *string
	Model                    *string
	ModelProvider            string
	SandboxPolicy            *string
	ReasoningEffort          *string
	ReasoningSummary         *string
	ServiceTier              string
	ApprovalPolicy           string
	ApprovalsReviewer        string
	SandboxNetworkAccess     bool
	CollaborationMode        *string
	Personality              *string
	WorkspaceKind            *string
	NumInputImages           int
	IsFirstTurn              bool
	Status                   *string
	TurnError                any
	CodexErrorKind           *string
	CodexErrorHTTPStatusCode *uint16
	SteerCount               *int
	ToolCounts               *CodexTurnToolCounts
	TokenUsage               *CodexTurnTokenUsage
	TimingProfile            CodexTurnTimingProfile
	DurationMS               *uint64
	StartedAt                *uint64
	CompletedAt              *uint64
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
			ThreadID:                  input.ThreadID,
			SessionID:                 input.SessionID,
			TurnID:                    input.TurnID,
			SubmissionType:            input.SubmissionType,
			AppServerClient:           client,
			Runtime:                   input.Runtime,
			Ephemeral:                 input.Ephemeral,
			ThreadSource:              input.ThreadSource,
			InitializationMode:        initializationMode,
			SubagentSource:            input.SubagentSource,
			ParentThreadID:            input.ParentThreadID,
			Model:                     input.Model,
			ModelProvider:             input.ModelProvider,
			SandboxPolicy:             input.SandboxPolicy,
			ReasoningEffort:           input.ReasoningEffort,
			ReasoningSummary:          input.ReasoningSummary,
			ServiceTier:               serviceTier,
			ApprovalPolicy:            input.ApprovalPolicy,
			ApprovalsReviewer:         input.ApprovalsReviewer,
			SandboxNetworkAccess:      input.SandboxNetworkAccess,
			CollaborationMode:         input.CollaborationMode,
			Personality:               input.Personality,
			WorkspaceKind:             input.WorkspaceKind,
			NumInputImages:            input.NumInputImages,
			IsFirstTurn:               input.IsFirstTurn,
			Status:                    input.Status,
			TurnError:                 input.TurnError,
			CodexErrorKind:            input.CodexErrorKind,
			CodexErrorHTTPStatusCode:  input.CodexErrorHTTPStatusCode,
			SteerCount:                input.SteerCount,
			TotalToolCallCount:        toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.Total }),
			ShellCommandCount:         toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.ShellCommand }),
			FileChangeCount:           toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.FileChange }),
			MCPToolCallCount:          toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.MCPToolCall }),
			DynamicToolCallCount:      toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.DynamicToolCall }),
			SubagentToolCallCount:     toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.SubagentToolCall }),
			WebSearchCount:            toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.WebSearch }),
			ImageGenerationCount:      toolCountPtr(input.ToolCounts, func(c CodexTurnToolCounts) int { return c.ImageGeneration }),
			InputTokens:               tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.InputTokens }),
			CachedInputTokens:         tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.CachedInputTokens }),
			OutputTokens:              tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.OutputTokens }),
			ReasoningOutputTokens:     tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.ReasoningOutputTokens }),
			TotalTokens:               tokenUsagePtr(input.TokenUsage, func(u CodexTurnTokenUsage) int64 { return u.TotalTokens }),
			BeforeFirstSamplingMS:     input.TimingProfile.BeforeFirstSamplingMS,
			SamplingMS:                input.TimingProfile.SamplingMS,
			BetweenSamplingOverheadMS: input.TimingProfile.BetweenSamplingOverheadMS,
			ToolBlockingMS:            input.TimingProfile.ToolBlockingMS,
			AfterLastSamplingMS:       input.TimingProfile.AfterLastSamplingMS,
			SamplingRequestCount:      input.TimingProfile.SamplingRequestCount,
			SamplingRetryCount:        input.TimingProfile.SamplingRetryCount,
			DurationMS:                input.DurationMS,
			StartedAt:                 input.StartedAt,
			CompletedAt:               input.CompletedAt,
		},
	}
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
