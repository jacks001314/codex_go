package appserver

import (
	"context"
	"strings"

	"codex_go/tool"
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
	model, permissionMode := a.attributionFor(invocation)
	result, err := a.Runner.RunPreToolUse(ctx, &HookPreToolUseRequest{
		ThreadID:       a.ThreadID,
		TurnID:         a.TurnID,
		Subagent:       a.Subagent,
		CWD:            a.CWD,
		TranscriptPath: a.TranscriptPath,
		Model:          model,
		PermissionMode: permissionMode,
		ToolName:       payload.ToolName.Name,
		MatcherAliases: append([]string(nil), payload.ToolName.MatcherAliases...),
		ToolUseID:      toolUseID,
		ToolInput:      payload.ToolInput,
		ToolCWD:        payload.ToolCWD,
		Hooks:          filterHooksForTargetEnvironments(a.Hooks, payload.TargetEnvironments),
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

// RunPermissionRequest runs the turn's PermissionRequest hooks for one approval
// action and returns the folded verdict (Rust
// hook_runtime::run_permission_request_hooks). A nil verdict means no matching
// hook decided, so the caller continues with its normal approval flow.
func (a *ToolHookAdapter) RunPermissionRequest(ctx context.Context, runIDSuffix string, toolName string, matcherAliases []string, toolInput any, toolCWD *string, targetEnvironment string) (*HookPermissionRequestDecision, error) {
	if a == nil || a.Runner == nil {
		return nil, nil
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, nil
	}
	var targetEnvironments []string
	if environmentID := strings.TrimSpace(targetEnvironment); environmentID != "" {
		targetEnvironments = []string{environmentID}
	}
	result, err := a.Runner.RunPermissionRequest(ctx, &HookPermissionRequestRequest{
		ThreadID:       a.ThreadID,
		TurnID:         a.TurnID,
		Subagent:       a.Subagent,
		CWD:            a.CWD,
		TranscriptPath: a.TranscriptPath,
		Model:          a.Model,
		PermissionMode: firstHookString(a.PermissionMode, "default"),
		ToolName:       toolName,
		MatcherAliases: append([]string(nil), matcherAliases...),
		RunIDSuffix:    strings.TrimSpace(runIDSuffix),
		ToolInput:      toolInput,
		ToolCWD:        toolCWD,
		Hooks:          filterHooksForTargetEnvironments(a.Hooks, targetEnvironments),
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.PermissionRequestDecision, nil
}

func (a *ToolHookAdapter) RunPostToolUse(ctx context.Context, invocation *tool.Invocation, payload *tool.PostToolUsePayload) (*tool.PostToolUseHookOutcome, error) {
	if a == nil || a.Runner == nil || payload == nil || payload.ToolName == nil {
		return nil, nil
	}
	toolUseID := payload.ToolUseID
	if strings.TrimSpace(toolUseID) == "" && invocation != nil {
		toolUseID = invocation.CallID
	}
	model, permissionMode := a.attributionFor(invocation)
	result, err := a.Runner.RunPostToolUse(ctx, &HookPostToolUseRequest{
		ThreadID:       a.ThreadID,
		TurnID:         a.TurnID,
		Subagent:       a.Subagent,
		CWD:            a.CWD,
		TranscriptPath: a.TranscriptPath,
		Model:          model,
		PermissionMode: permissionMode,
		ToolName:       payload.ToolName.Name,
		MatcherAliases: append([]string(nil), payload.ToolName.MatcherAliases...),
		ToolUseID:      toolUseID,
		ToolInput:      payload.ToolInput,
		ToolResponse:   payload.ToolResponse,
		ToolCWD:        payload.ToolCWD,
		Hooks:          filterHooksForTargetEnvironments(a.Hooks, payload.TargetEnvironments),
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

// attributionFor prefers the settings captured on the invocation by the tool
// dispatcher (Rust #45248 runs pre/post hooks with step_context.settings) and
// falls back to the adapter's turn-level attribution for calls dispatched
// without a captured step.
func (a *ToolHookAdapter) attributionFor(invocation *tool.Invocation) (string, string) {
	model := a.Model
	permissionMode := firstHookString(a.PermissionMode, "default")
	if invocation != nil && invocation.StepAttribution != nil {
		model = firstHookString(invocation.StepAttribution.Model, model)
		permissionMode = firstHookString(invocation.StepAttribution.PermissionMode, permissionMode)
	}
	return model, permissionMode
}

func firstHookString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// filterHooksForTargetEnvironments scopes environment-owned managed hooks to
// the environments the call targets (Rust
// hook_environment_executor::environment_hook_sources). An empty target list
// keeps every environment hook eligible (Rust `ToolEnvironments::All`), and
// hooks that are not environment-owned always stay.
func filterHooksForTargetEnvironments(hooks []HookMetadata, targetEnvironments []string) []HookMetadata {
	if len(hooks) == 0 || len(targetEnvironments) == 0 {
		return hooks
	}
	targets := make(map[string]struct{}, len(targetEnvironments))
	for _, id := range targetEnvironments {
		if id = strings.TrimSpace(id); id != "" {
			targets[id] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return hooks
	}
	filtered := make([]HookMetadata, 0, len(hooks))
	for _, hook := range hooks {
		if hook.EnvironmentID != nil {
			if _, ok := targets[strings.TrimSpace(*hook.EnvironmentID)]; !ok {
				continue
			}
		}
		filtered = append(filtered, hook)
	}
	return filtered
}
