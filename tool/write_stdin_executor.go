package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const DefaultWriteStdinToolName = "write_stdin"

type WriteStdinExecutor struct {
	manager          *UnifiedExecManager
	maxOutputTokens  *int
	environmentCheck *UnifiedExecEnvironmentCheck
}

// WriteStdinOptions configures the write_stdin executor.
type WriteStdinOptions struct {
	Manager         *UnifiedExecManager
	MaxOutputTokens *int
	// EnvironmentCheck mirrors the turn's environment readiness. Rust #50962
	// only resolves the tool's environment while `stable_environment_tools` is
	// on (the default-off path skips the check, so an existing session stays
	// writable while readiness changes).
	EnvironmentCheck *UnifiedExecEnvironmentCheck
}

func NewWriteStdinExecutor(manager *UnifiedExecManager, maxOutputTokens *int) *WriteStdinExecutor {
	return NewWriteStdinExecutorWithOptions(&WriteStdinOptions{Manager: manager, MaxOutputTokens: maxOutputTokens})
}

// NewWriteStdinExecutorWithOptions builds the write_stdin executor with the
// turn's environment readiness facts.
func NewWriteStdinExecutorWithOptions(options *WriteStdinOptions) *WriteStdinExecutor {
	if options == nil {
		options = &WriteStdinOptions{}
	}
	return &WriteStdinExecutor{
		manager:          options.Manager,
		maxOutputTokens:  cloneNonNegativeInt(options.MaxOutputTokens),
		environmentCheck: cloneUnifiedExecEnvironmentCheck(options.EnvironmentCheck),
	}
}

func RegisterWriteStdinHandler(registry *Registry, manager *UnifiedExecManager, maxOutputTokens *int) error {
	return RegisterWriteStdinHandlerWithOptions(registry, &WriteStdinOptions{Manager: manager, MaxOutputTokens: maxOutputTokens})
}

// RegisterWriteStdinHandlerWithOptions registers write_stdin with the turn's
// environment readiness facts (Rust #50962's flag-on environment check).
func RegisterWriteStdinHandlerWithOptions(registry *Registry, options *WriteStdinOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	if options == nil || options.Manager == nil {
		return nil
	}
	return registry.Register(NewWriteStdinExecutorWithOptions(options))
}

// stableEnvironmentTools reports whether the default-off
// `stable_environment_tools` feature is on for this turn (Rust #50962).
func (e *WriteStdinExecutor) stableEnvironmentTools() bool {
	return e != nil && e.environmentCheck != nil && e.environmentCheck.StableEnvironmentTools
}

// hasUsableEnvironment reports whether the turn has a usable selected
// environment; only meaningful with the host-supplied readiness facts.
func (e *WriteStdinExecutor) hasUsableEnvironment() bool {
	return e != nil && e.environmentCheck != nil && e.environmentCheck.ReadyEnvironmentCount > 0
}

func (e *WriteStdinExecutor) Spec() Spec {
	return Spec{
		Name:        PlainName(DefaultWriteStdinToolName),
		Description: "Writes characters to an existing unified exec session and returns recent output.",
		InputSchema: map[string]any{
			"type":                 "object",
			"required":             []string{"session_id"},
			"additionalProperties": false,
			"properties": map[string]any{
				"session_id":        map[string]any{"type": "number", "description": "Identifier of the running unified exec session."},
				"chars":             map[string]any{"type": "string", "description": "Bytes to write to stdin. Defaults to empty, which polls without writing."},
				"yield_time_ms":     map[string]any{"type": "number", "description": "Wait before yielding output. Non-empty writes default to 250 ms and cap at 30000 ms; empty polls wait 5000-300000 ms by default."},
				"max_output_tokens": map[string]any{"type": "number", "description": "Output token budget. Defaults to 10000 tokens; larger requests may be capped by policy."},
			},
		},
		OutputSchema: unifiedExecOutputSchema(),
		Parallel:     true,
	}
}

func (e *WriteStdinExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	if e == nil || e.manager == nil {
		return nil, RespondToModel("write_stdin failed: " + UnifiedExecUnavailableMessage)
	}
	// Rust #50962: write_stdin only resolves the tool's environment while
	// `stable_environment_tools` is on; the default path deliberately skips the
	// readiness check so an in-flight session stays writable.
	if e.stableEnvironmentTools() && !e.hasUsableEnvironment() {
		return nil, RespondToModel(UnifiedUnavailableEnvironmentMessage)
	}
	var args WriteStdinArgs
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	if args.YieldTimeMS == 0 {
		args.YieldTimeMS = DefaultWriteYieldTimeMS
	}
	args.CallID = strings.TrimSpace(invocation.CallID)
	result, err := e.manager.WriteStdin(ctx, &args, e.maxOutputTokens)
	if err != nil {
		var approvalErr *UnifiedExecStdinApprovalError
		if errors.As(err, &approvalErr) {
			return nil, RespondToModel("write_stdin rejected: " + approvalErr.Message)
		}
		return nil, RespondToModel("write_stdin failed: " + err.Error())
	}
	maxOutputTokens := result.MaxOutputTokensUsed
	body := shellResultModelTextWithMetadata(result, maxOutputTokens, result.ChunkID)
	return &Output{
		Success:    true,
		Body:       body,
		Data:       shellResultData(result, maxOutputTokens, result.ChunkID),
		LogPreview: shellLogPreview(body),
	}, nil
}

func (e *WriteStdinExecutor) PreToolUsePayload(_ *Invocation) (*PreToolUsePayload, bool) {
	return nil, false
}

func (e *WriteStdinExecutor) PostToolUsePayload(_ *Invocation, output *Output) (*PostToolUsePayload, bool) {
	if output == nil || output.Data == nil || output.Data["process_id"] != nil {
		return nil, false
	}
	hookCommand, _ := output.Data["hook_command"].(string)
	originalCallID, _ := output.Data["event_call_id"].(string)
	response, _ := output.Data["hook_response"].(string)
	if hookCommand == "" || originalCallID == "" {
		return nil, false
	}
	return &PostToolUsePayload{
		ToolName:     bashHookToolName(),
		ToolUseID:    originalCallID,
		ToolInput:    map[string]any{"command": hookCommand},
		ToolResponse: response,
	}, true
}

var _ Executor = (*WriteStdinExecutor)(nil)
var _ PreToolUsePayloadProvider = (*WriteStdinExecutor)(nil)
var _ PostToolUsePayloadProvider = (*WriteStdinExecutor)(nil)
