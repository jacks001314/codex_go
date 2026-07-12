package tool

import (
	"context"
	"fmt"
)

const DefaultWriteStdinToolName = "write_stdin"

type WriteStdinExecutor struct {
	manager         *UnifiedExecManager
	maxOutputTokens *int
}

func NewWriteStdinExecutor(manager *UnifiedExecManager, maxOutputTokens *int) *WriteStdinExecutor {
	return &WriteStdinExecutor{manager: manager, maxOutputTokens: cloneNonNegativeInt(maxOutputTokens)}
}

func RegisterWriteStdinHandler(registry *Registry, manager *UnifiedExecManager, maxOutputTokens *int) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	if manager == nil {
		return nil
	}
	return registry.Register(NewWriteStdinExecutor(manager, maxOutputTokens))
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
		return nil, RespondToModel("write_stdin failed: unified exec is unavailable in this session")
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
	result, err := e.manager.WriteStdin(ctx, &args, e.maxOutputTokens)
	if err != nil {
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
