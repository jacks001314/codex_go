package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type HookToolName struct {
	Name           string
	MatcherAliases []string
}

type PreToolUsePayload struct {
	ToolName  *HookToolName
	ToolInput any
	// McpTool, when set, carries read-only metadata and provenance captured
	// from the MCP call that will execute (Rust ToolStartInput.mcp_tool,
	// #40976). It exposes the model-visible MCP tool details and source
	// classification without exposing the executable client.
	McpTool *McpToolContext
}

// McpToolSource classifies the origin of an MCP tool call for tool lifecycle
// extensions (Rust extension_api::McpToolSource, #40976).
type McpToolSource string

const (
	McpToolSourceConnector      McpToolSource = "connector"
	McpToolSourceConfig         McpToolSource = "config"
	McpToolSourceSelectedPlugin McpToolSource = "selected_plugin"
	McpToolSourcePlugin         McpToolSource = "plugin"
	McpToolSourceOther          McpToolSource = "other"
)

// McpToolContext is the model-visible MCP tool details plus its source
// classification, captured from the MCP call that will execute (Rust
// extension_api::McpToolContext, #40976).
type McpToolContext struct {
	ServerName string        `json:"serverName,omitempty"`
	ToolName   string        `json:"toolName,omitempty"`
	Connector  string        `json:"connector,omitempty"`
	PluginID   string        `json:"pluginId,omitempty"`
	Source     McpToolSource `json:"source,omitempty"`
}

type PostToolUsePayload struct {
	ToolName     *HookToolName
	ToolUseID    string
	ToolInput    any
	ToolResponse any
}

type PreToolUseHookOutcome struct {
	Blocked            bool
	BlockReason        string
	Fatal              bool
	FatalReason        string
	UpdatedInput       any
	AdditionalContexts []string
}

type PostToolUseHookOutcome struct {
	Blocked            bool
	FeedbackMessage    string
	Fatal              bool
	FatalReason        string
	AdditionalContexts []string
}

type HookRunner interface {
	RunPreToolUse(ctx context.Context, invocation *Invocation, payload *PreToolUsePayload) (*PreToolUseHookOutcome, error)
	RunPostToolUse(ctx context.Context, invocation *Invocation, payload *PostToolUsePayload) (*PostToolUseHookOutcome, error)
}

type PreToolUsePayloadProvider interface {
	PreToolUsePayload(invocation *Invocation) (*PreToolUsePayload, bool)
}

type PostToolUsePayloadProvider interface {
	PostToolUsePayload(invocation *Invocation, output *Output) (*PostToolUsePayload, bool)
}

type HookInputUpdater interface {
	WithUpdatedHookInput(invocation *Invocation, updatedInput any) (*Invocation, error)
}

func (r *Router) DispatchWithHooks(ctx context.Context, invocation *Invocation, hooks HookRunner) (*Output, error) {
	return r.DispatchWithHooksAfterPreHooks(ctx, invocation, hooks, nil)
}

// DispatchWithHooksAfterPreHooks runs the tool through pre-tool hooks and then
// executes it, invoking onStarted (when non-nil) after pre-tool hooks complete
// and before the executor runs (Rust #38568). The callback receives the
// possibly hook-rewritten invocation, mirroring Rust's tool lifecycle ordering.
func (r *Router) DispatchWithHooksAfterPreHooks(ctx context.Context, invocation *Invocation, hooks HookRunner, onStarted func(*Invocation)) (*Output, error) {
	if hooks == nil {
		if onStarted != nil {
			onStarted(invocation)
		}
		return r.Dispatch(ctx, invocation)
	}
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	executor, ok := r.registry.Lookup(invocation.ToolName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, invocation.ToolName.Key())
	}
	select {
	case <-ctx.Done():
		return nil, ErrToolCancelled
	default:
	}

	current := cloneInvocation(invocation)
	if payload, ok := preToolUsePayload(executor, current); ok {
		outcome, err := hooks.RunPreToolUse(ctx, current, payload)
		if err != nil {
			return nil, hookRunnerError(err)
		}
		if outcome != nil && outcome.Fatal {
			return nil, Fatal(hookFatalMessage("PreToolUse", outcome.FatalReason))
		}
		if outcome != nil && outcome.Blocked {
			return nil, RespondToModel(preToolUseBlockMessage(current, outcome.BlockReason))
		}
		if outcome != nil && len(outcome.AdditionalContexts) > 0 {
			appendInvocationHookContexts(current, outcome.AdditionalContexts)
		}
		if outcome != nil && outcome.UpdatedInput != nil {
			updated, err := updatedHookInvocation(executor, current, outcome.UpdatedInput)
			if err != nil {
				return nil, err
			}
			current = updated
		}
	}

	if onStarted != nil {
		onStarted(current)
	}
	output, err := executor.Execute(ctx, current)
	if err != nil {
		return nil, err
	}
	output = normalizeOutput(current, output, r.now)

	if output.Success {
		if payload, ok := postToolUsePayload(executor, current, output); ok {
			outcome, err := hooks.RunPostToolUse(ctx, current, payload)
			if err != nil {
				return nil, hookRunnerError(err)
			}
			if outcome != nil && outcome.Fatal {
				return nil, Fatal(hookFatalMessage("PostToolUse", outcome.FatalReason))
			}
			if outcome != nil && outcome.Blocked {
				message := strings.TrimSpace(outcome.FeedbackMessage)
				if message == "" {
					message = "PostToolUse hook blocked the tool result"
				}
				return nil, RespondToModel(message)
			}
			if outcome != nil && strings.TrimSpace(outcome.FeedbackMessage) != "" {
				output.Body = strings.TrimSpace(outcome.FeedbackMessage)
			}
			if outcome != nil && len(outcome.AdditionalContexts) > 0 {
				appendOutputHookContexts(output, outcome.AdditionalContexts)
			}
		}
	}
	return output, nil
}

func preToolUsePayload(executor Executor, invocation *Invocation) (*PreToolUsePayload, bool) {
	if provider, ok := executor.(PreToolUsePayloadProvider); ok {
		return provider.PreToolUsePayload(invocation)
	}
	if invocation == nil || invocation.Payload.Kind != PayloadFunction {
		return nil, false
	}
	return &PreToolUsePayload{
		ToolName:  hookToolName(invocation.ToolName),
		ToolInput: functionHookInput(invocation.Payload.Arguments),
	}, true
}

func postToolUsePayload(executor Executor, invocation *Invocation, output *Output) (*PostToolUsePayload, bool) {
	if provider, ok := executor.(PostToolUsePayloadProvider); ok {
		return provider.PostToolUsePayload(invocation, output)
	}
	if invocation == nil || output == nil || invocation.Payload.Kind != PayloadFunction {
		return nil, false
	}
	return &PostToolUsePayload{
		ToolName:     hookToolName(invocation.ToolName),
		ToolUseID:    firstNonEmptyString(output.CallID, invocation.CallID),
		ToolInput:    functionHookInput(invocation.Payload.Arguments),
		ToolResponse: outputHookResponse(output),
	}, true
}

func updatedHookInvocation(executor Executor, invocation *Invocation, updatedInput any) (*Invocation, error) {
	if updater, ok := executor.(HookInputUpdater); ok {
		return updater.WithUpdatedHookInput(invocation, updatedInput)
	}
	if invocation == nil || invocation.Payload.Kind != PayloadFunction {
		return nil, fmt.Errorf("%w: hook input rewrite received unsupported payload", ErrToolInvalidCall)
	}
	data, err := json.Marshal(updatedInput)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("failed to serialize rewritten %s arguments: %v", invocation.ToolName.Key(), err))
	}
	updated := cloneInvocation(invocation)
	updated.Payload.Arguments = string(data)
	return updated, nil
}

func normalizeOutput(invocation *Invocation, output *Output, now func() time.Time) *Output {
	if output == nil {
		output = &Output{Success: true}
	}
	if invocation != nil {
		output.CallID = invocation.CallID
		output.ToolName = invocation.ToolName
	}
	if output.CompletedAt.IsZero() {
		if now == nil {
			now = time.Now
		}
		output.CompletedAt = now().UTC()
	}
	return output
}

func hookToolName(name ToolName) *HookToolName {
	return &HookToolName{Name: name.Key()}
}

func functionHookInput(arguments string) any {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return arguments
	}
	return value
}

func outputHookResponse(output *Output) any {
	if output == nil {
		return nil
	}
	if output.Data != nil {
		return output.Data
	}
	if strings.TrimSpace(output.Body) != "" {
		return output.Body
	}
	if strings.TrimSpace(output.Error) != "" {
		return map[string]any{"error": output.Error}
	}
	return map[string]any{"success": output.Success}
}

func hookRunnerError(err error) error {
	if err == nil {
		return nil
	}
	callErr := FromError(err)
	if callErr == nil {
		return nil
	}
	return callErr
}

func hookFatalMessage(event string, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return event + " hook stopped execution"
	}
	return event + " hook stopped execution: " + reason
}

func appendInvocationHookContexts(invocation *Invocation, contexts []string) {
	trimmed := trimHookContexts(contexts)
	if invocation == nil || len(trimmed) == 0 {
		return
	}
	if invocation.Context == nil {
		invocation.Context = map[string]any{}
	}
	invocation.Context["additional_contexts"] = appendStringContextValue(invocation.Context["additional_contexts"], trimmed)
}

func appendOutputHookContexts(output *Output, contexts []string) {
	trimmed := trimHookContexts(contexts)
	if output == nil || len(trimmed) == 0 {
		return
	}
	if output.Data == nil {
		output.Data = map[string]any{}
	}
	output.Data["additional_contexts"] = appendStringContextValue(output.Data["additional_contexts"], trimmed)
	contextText := strings.Join(trimmed, "\n\n")
	if strings.TrimSpace(output.Body) == "" {
		output.Body = contextText
		return
	}
	output.Body = strings.TrimRight(output.Body, "\r\n") + "\n\nAdditional context from PostToolUse hook:\n" + contextText
}

func trimHookContexts(contexts []string) []string {
	out := make([]string, 0, len(contexts))
	for _, context := range contexts {
		context = strings.TrimSpace(context)
		if context != "" {
			out = append(out, context)
		}
	}
	return out
}

func appendStringContextValue(existing any, values []string) []string {
	out := make([]string, 0, len(values))
	switch typed := existing.(type) {
	case []string:
		out = append(out, typed...)
	case []any:
		for _, value := range typed {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
	case string:
		if strings.TrimSpace(typed) != "" {
			out = append(out, strings.TrimSpace(typed))
		}
	}
	out = append(out, values...)
	return out
}

func preToolUseBlockMessage(invocation *Invocation, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "blocked by hook"
	}
	toolName := ""
	if invocation != nil {
		toolName = invocation.ToolName.Key()
		if input, ok := functionHookInput(invocation.Payload.Arguments).(map[string]any); ok {
			if command, ok := input["command"].(string); ok && strings.TrimSpace(command) != "" {
				return fmt.Sprintf("Command blocked by PreToolUse hook: %s. Command: %s", reason, command)
			}
		}
	}
	return fmt.Sprintf("Tool call blocked by PreToolUse hook: %s. Tool: %s", reason, toolName)
}

func cloneInvocation(invocation *Invocation) *Invocation {
	if invocation == nil {
		return nil
	}
	clone := *invocation
	if invocation.Context != nil {
		clone.Context = make(map[string]any, len(invocation.Context))
		for key, value := range invocation.Context {
			clone.Context[key] = value
		}
	}
	return &clone
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
