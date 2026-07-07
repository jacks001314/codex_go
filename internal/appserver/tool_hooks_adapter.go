package appserver

import (
	"context"
	"strings"

	"codex_go/internal/tool"
)

type ToolHookAdapter struct {
	Runner         *HookRunner
	Hooks          []HookMetadata
	ThreadID       string
	TurnID         string
	CWD            string
	TranscriptPath *string
	Model          string
	PermissionMode string
	Subagent       *SubagentHookContext
}

func NewToolHookAdapter(runner *HookRunner, hooks []HookMetadata, threadID string, turnID string, cwd string) *ToolHookAdapter {
	return &ToolHookAdapter{
		Runner:         runner,
		Hooks:          append([]HookMetadata(nil), hooks...),
		ThreadID:       threadID,
		TurnID:         turnID,
		CWD:            cwd,
		PermissionMode: "default",
	}
}

func (a *ToolHookAdapter) RunPreToolUse(ctx context.Context, invocation *tool.Invocation, payload *tool.PreToolUsePayload) (*tool.PreToolUseHookOutcome, error) {
	if a == nil || a.Runner == nil || payload == nil || payload.ToolName == nil {
		return nil, nil
	}
	toolUseID := ""
	if invocation != nil {
		toolUseID = invocation.CallID
	}
	result, err := a.Runner.RunPreToolUse(ctx, &HookPreToolUseRequest{
		ThreadID:       a.ThreadID,
		TurnID:         a.TurnID,
		Subagent:       a.Subagent,
		CWD:            a.CWD,
		TranscriptPath: a.TranscriptPath,
		Model:          a.Model,
		PermissionMode: firstHookString(a.PermissionMode, "default"),
		ToolName:       payload.ToolName.Name,
		MatcherAliases: append([]string(nil), payload.ToolName.MatcherAliases...),
		ToolUseID:      toolUseID,
		ToolInput:      payload.ToolInput,
		Hooks:          a.Hooks,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &tool.PreToolUseHookOutcome{
		Blocked:            result.Blocked,
		BlockReason:        result.BlockReason,
		Fatal:              result.Stopped,
		FatalReason:        result.StopReason,
		UpdatedInput:       result.UpdatedInput,
		AdditionalContexts: append([]string(nil), result.AdditionalContexts...),
	}, nil
}

func (a *ToolHookAdapter) RunPostToolUse(ctx context.Context, invocation *tool.Invocation, payload *tool.PostToolUsePayload) (*tool.PostToolUseHookOutcome, error) {
	if a == nil || a.Runner == nil || payload == nil || payload.ToolName == nil {
		return nil, nil
	}
	toolUseID := payload.ToolUseID
	if strings.TrimSpace(toolUseID) == "" && invocation != nil {
		toolUseID = invocation.CallID
	}
	result, err := a.Runner.RunPostToolUse(ctx, &HookPostToolUseRequest{
		ThreadID:       a.ThreadID,
		TurnID:         a.TurnID,
		Subagent:       a.Subagent,
		CWD:            a.CWD,
		TranscriptPath: a.TranscriptPath,
		Model:          a.Model,
		PermissionMode: firstHookString(a.PermissionMode, "default"),
		ToolName:       payload.ToolName.Name,
		MatcherAliases: append([]string(nil), payload.ToolName.MatcherAliases...),
		ToolUseID:      toolUseID,
		ToolInput:      payload.ToolInput,
		ToolResponse:   payload.ToolResponse,
		Hooks:          a.Hooks,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &tool.PostToolUseHookOutcome{
		Blocked:            result.Blocked,
		FeedbackMessage:    firstHookString(result.FeedbackMessage, result.BlockReason),
		Fatal:              result.Stopped,
		FatalReason:        result.StopReason,
		AdditionalContexts: append([]string(nil), result.AdditionalContexts...),
	}, nil
}

func firstHookString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
