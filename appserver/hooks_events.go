package appserver

import (
	"context"
	"fmt"
)

type SessionStartSource string

const (
	SessionStartSourceStartup SessionStartSource = "startup"
	SessionStartSourceResume  SessionStartSource = "resume"
	SessionStartSourceClear   SessionStartSource = "clear"
	SessionStartSourceCompact SessionStartSource = "compact"
)

type SubagentHookContext struct {
	AgentID   string
	AgentType string
}

type HookSessionStartRequest struct {
	ThreadID       string
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	Source         SessionStartSource
	Hooks          []HookMetadata
}

type HookSubagentStartRequest struct {
	ThreadID       string
	TurnID         string
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	AgentID        string
	AgentType      string
	Hooks          []HookMetadata
}

type HookStopRequest struct {
	ThreadID             string
	TurnID               string
	CWD                  string
	TranscriptPath       *string
	Model                string
	PermissionMode       string
	StopHookActive       bool
	LastAssistantMessage *string
	Hooks                []HookMetadata
}

type HookSubagentStopRequest struct {
	ThreadID             string
	TurnID               string
	CWD                  string
	TranscriptPath       *string
	AgentTranscriptPath  *string
	Model                string
	PermissionMode       string
	StopHookActive       bool
	AgentID              string
	AgentType            string
	LastAssistantMessage *string
	Hooks                []HookMetadata
}

type HookPreToolUseRequest struct {
	ThreadID       string
	TurnID         string
	Subagent       *SubagentHookContext
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	ToolName       string
	MatcherAliases []string
	ToolUseID      string
	ToolInput      any
	Hooks          []HookMetadata
}

type HookPermissionRequestRequest struct {
	ThreadID       string
	TurnID         string
	Subagent       *SubagentHookContext
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	ToolName       string
	MatcherAliases []string
	RunIDSuffix    string
	ToolInput      any
	Hooks          []HookMetadata
}

type HookPostToolUseRequest struct {
	ThreadID       string
	TurnID         string
	Subagent       *SubagentHookContext
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	ToolName       string
	MatcherAliases []string
	ToolUseID      string
	ToolInput      any
	ToolResponse   any
	Hooks          []HookMetadata
}

type HookPreCompactRequest struct {
	ThreadID       string
	TurnID         string
	Subagent       *SubagentHookContext
	CWD            string
	TranscriptPath *string
	Model          string
	Trigger        string
	Hooks          []HookMetadata
}

type HookPostCompactRequest struct {
	ThreadID       string
	TurnID         string
	Subagent       *SubagentHookContext
	CWD            string
	TranscriptPath *string
	Model          string
	Trigger        string
	Hooks          []HookMetadata
}

type HookUserPromptSubmitRequest struct {
	ThreadID       string
	TurnID         string
	Subagent       *SubagentHookContext
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	Prompt         string
	Hooks          []HookMetadata
}

type HookSessionEndRequest struct {
	ThreadID       string
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	Reason         string
	Hooks          []HookMetadata
}

func (r *HookRunner) RunSessionStart(ctx context.Context, request *HookSessionStartRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: session start hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "SessionStart",
		"model":           request.Model,
		"permission_mode": request.PermissionMode,
		"source":          string(request.Source),
	}
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		CWD:           request.CWD,
		EventName:     HookEventSessionStart,
		MatcherInputs: []string{string(request.Source)},
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunSessionEnd(ctx context.Context, request *HookSessionEndRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: session end hook request is nil", ErrInvalidHook)
	}
	inputJSON, err := hookInputJSON(map[string]any{
		"session_id": request.ThreadID, "transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd": request.CWD, "hook_event_name": "SessionEnd", "model": request.Model,
		"permission_mode": request.PermissionMode, "reason": request.Reason,
	})
	if err != nil {
		return nil, err
	}
	return r.Run(ctx, &HookRunRequest{ThreadID: request.ThreadID, CWD: request.CWD, EventName: HookEventSessionEnd, InputJSON: inputJSON, Hooks: request.Hooks})
}

func (r *HookRunner) RunSubagentStart(ctx context.Context, request *HookSubagentStartRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: subagent start hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "SubagentStart",
		"model":           request.Model,
		"permission_mode": request.PermissionMode,
		"agent_id":        request.AgentID,
		"agent_type":      request.AgentType,
	}
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventSubagentStart,
		MatcherInputs: []string{request.AgentType},
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunStop(ctx context.Context, request *HookStopRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: stop hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":             request.ThreadID,
		"turn_id":                request.TurnID,
		"transcript_path":        nullableStringValue(request.TranscriptPath),
		"cwd":                    request.CWD,
		"hook_event_name":        "Stop",
		"model":                  request.Model,
		"permission_mode":        request.PermissionMode,
		"stop_hook_active":       request.StopHookActive,
		"last_assistant_message": nullableStringValue(request.LastAssistantMessage),
	}
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:  request.ThreadID,
		TurnID:    &turnID,
		CWD:       request.CWD,
		EventName: HookEventStop,
		InputJSON: inputJSON,
		Hooks:     request.Hooks,
	})
}

func (r *HookRunner) RunSubagentStop(ctx context.Context, request *HookSubagentStopRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: subagent stop hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":             request.ThreadID,
		"turn_id":                request.TurnID,
		"transcript_path":        nullableStringValue(request.TranscriptPath),
		"agent_transcript_path":  nullableStringValue(request.AgentTranscriptPath),
		"cwd":                    request.CWD,
		"hook_event_name":        "SubagentStop",
		"model":                  request.Model,
		"permission_mode":        request.PermissionMode,
		"stop_hook_active":       request.StopHookActive,
		"agent_id":               request.AgentID,
		"agent_type":             request.AgentType,
		"last_assistant_message": nullableStringValue(request.LastAssistantMessage),
	}
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventSubagentStop,
		MatcherInputs: []string{request.AgentType},
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunPreToolUse(ctx context.Context, request *HookPreToolUseRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: pre tool use hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "PreToolUse",
		"model":           request.Model,
		"permission_mode": request.PermissionMode,
		"tool_name":       request.ToolName,
		"tool_input":      request.ToolInput,
		"tool_use_id":     request.ToolUseID,
	}
	addSubagentHookInput(input, request.Subagent)
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventPreToolUse,
		MatcherInputs: toolMatcherInputs(request.ToolName, request.MatcherAliases),
		RunIDSuffix:   request.ToolUseID,
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunPermissionRequest(ctx context.Context, request *HookPermissionRequestRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: permission request hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "PermissionRequest",
		"model":           request.Model,
		"permission_mode": request.PermissionMode,
		"tool_name":       request.ToolName,
		"tool_input":      request.ToolInput,
	}
	addSubagentHookInput(input, request.Subagent)
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventPermissionRequest,
		MatcherInputs: toolMatcherInputs(request.ToolName, request.MatcherAliases),
		RunIDSuffix:   request.RunIDSuffix,
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunPostToolUse(ctx context.Context, request *HookPostToolUseRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: post tool use hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "PostToolUse",
		"model":           request.Model,
		"permission_mode": request.PermissionMode,
		"tool_name":       request.ToolName,
		"tool_input":      request.ToolInput,
		"tool_response":   request.ToolResponse,
		"tool_use_id":     request.ToolUseID,
	}
	addSubagentHookInput(input, request.Subagent)
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventPostToolUse,
		MatcherInputs: toolMatcherInputs(request.ToolName, request.MatcherAliases),
		RunIDSuffix:   request.ToolUseID,
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunPreCompact(ctx context.Context, request *HookPreCompactRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: pre compact hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "PreCompact",
		"model":           request.Model,
		"trigger":         request.Trigger,
	}
	addSubagentHookInput(input, request.Subagent)
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventPreCompact,
		MatcherInputs: []string{request.Trigger},
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunPostCompact(ctx context.Context, request *HookPostCompactRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: post compact hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "PostCompact",
		"model":           request.Model,
		"trigger":         request.Trigger,
	}
	addSubagentHookInput(input, request.Subagent)
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:      request.ThreadID,
		TurnID:        &turnID,
		CWD:           request.CWD,
		EventName:     HookEventPostCompact,
		MatcherInputs: []string{request.Trigger},
		InputJSON:     inputJSON,
		Hooks:         request.Hooks,
	})
}

func (r *HookRunner) RunUserPromptSubmit(ctx context.Context, request *HookUserPromptSubmitRequest) (*HookRunResult, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: user prompt submit hook request is nil", ErrInvalidHook)
	}
	input := map[string]any{
		"session_id":      request.ThreadID,
		"turn_id":         request.TurnID,
		"transcript_path": nullableStringValue(request.TranscriptPath),
		"cwd":             request.CWD,
		"hook_event_name": "UserPromptSubmit",
		"model":           request.Model,
		"permission_mode": request.PermissionMode,
		"prompt":          request.Prompt,
	}
	addSubagentHookInput(input, request.Subagent)
	inputJSON, err := hookInputJSON(input)
	if err != nil {
		return nil, err
	}
	turnID := request.TurnID
	return r.Run(ctx, &HookRunRequest{
		ThreadID:  request.ThreadID,
		TurnID:    &turnID,
		CWD:       request.CWD,
		EventName: HookEventUserPromptSubmit,
		InputJSON: inputJSON,
		Hooks:     request.Hooks,
	})
}

func nullableStringValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func addSubagentHookInput(input map[string]any, subagent *SubagentHookContext) {
	if subagent == nil {
		return
	}
	input["agent_id"] = subagent.AgentID
	input["agent_type"] = subagent.AgentType
}

func toolMatcherInputs(toolName string, aliases []string) []string {
	out := make([]string, 0, 1+len(aliases))
	out = append(out, toolName)
	out = append(out, aliases...)
	return out
}
