package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/internal/sandbox"
	"codex_go/internal/utils"
)

const (
	DefaultExecCommandToolName        = "exec_command"
	DefaultExecCommandMaxOutputTokens = 10000
)

type ShellExecutorOptions struct {
	Runner     ShellRunner
	Shell      *Shell
	Validation ShellValidationOptions
	ToolName   ToolName
	Approval   ShellApprovalFunc
}

type ShellExecutor struct {
	runner     ShellRunner
	shell      *Shell
	validation ShellValidationOptions
	toolName   ToolName
	approval   ShellApprovalFunc
}

type ShellApprovalDecision struct {
	Approved     bool
	AllowSession bool
}

type ShellApprovalRequest struct {
	Request    *ShellRequest
	Invocation *Invocation
}

type ShellApprovalFunc func(context.Context, *ShellApprovalRequest) (ShellApprovalDecision, error)

func NewShellExecutor(options *ShellExecutorOptions) *ShellExecutor {
	executor := &ShellExecutor{
		runner:     NewLocalShellRunner(),
		shell:      NewDefaultShell(),
		validation: ShellValidationOptions{ApprovalPolicy: sandbox.ApprovalOnRequest},
		toolName:   PlainName(DefaultExecCommandToolName),
	}
	if options == nil {
		return executor
	}
	if options.Runner != nil {
		executor.runner = options.Runner
	}
	if options.Shell != nil {
		executor.shell = options.Shell
	}
	executor.validation = options.Validation
	if executor.validation.ApprovalPolicy == "" {
		executor.validation.ApprovalPolicy = sandbox.ApprovalOnRequest
	}
	if options.ToolName.Key() != "" {
		executor.toolName = options.ToolName
	}
	executor.approval = options.Approval
	return executor
}

func RegisterShellHandler(registry *Registry, options *ShellExecutorOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	return registry.Register(NewShellExecutor(options))
}

func (e *ShellExecutor) Spec() Spec {
	if e == nil || e.toolName.Key() == "" {
		return Spec{Name: PlainName(DefaultExecCommandToolName)}
	}
	return Spec{
		Name:        e.toolName,
		Description: "Runs a shell command in the current workspace.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"cmd"},
			"properties": map[string]any{
				"cmd": map[string]any{
					"type":        "string",
					"description": "The shell command to execute.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Relative paths are resolved from the session cwd.",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Alias for cwd.",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"timeout_ms":    map[string]any{"type": "integer", "minimum": 0},
				"yield_time_ms": map[string]any{"type": "integer", "minimum": 0},
				"max_output_tokens": map[string]any{
					"type":    "integer",
					"minimum": 0,
				},
				"shell":               map[string]any{"type": "string"},
				"login":               map[string]any{"type": "boolean"},
				"tty":                 map[string]any{"type": "boolean"},
				"sandbox_permissions": map[string]any{"type": "string"},
				"additional_permissions": map[string]any{
					"type": "object",
				},
				"justification": map[string]any{"type": "string"},
				"prefix_rule":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		Parallel: true,
	}
}

func (e *ShellExecutor) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	args, err := decodeExecCommandInvocation(invocation)
	if err != nil {
		return nil, err
	}
	validation := e.validationOptions()
	if invocationPermissionPreapproved(invocation) {
		validation.PermissionsPreapproved = true
	}
	req, err := BuildShellRequest(args, e.sessionShell(), validation)
	if err != nil {
		return nil, RespondToModel(err.Error())
	}
	if req.ApprovalRequired {
		if e.approval != nil {
			decision, approvalErr := e.approval(ctx, &ShellApprovalRequest{Request: req, Invocation: invocation})
			if approvalErr != nil {
				body := "Approval request failed: " + approvalErr.Error()
				return &Output{
					Success:    false,
					Body:       body,
					Error:      body,
					Data:       shellApprovalDeniedData(req, "error"),
					LogPreview: shellLogPreview(body),
				}, nil
			}
			if !decision.Approved {
				body := "Approval denied before running command."
				return &Output{
					Success:    false,
					Body:       body,
					Error:      body,
					Data:       shellApprovalDeniedData(req, "deny"),
					LogPreview: shellLogPreview(body),
				}, nil
			}
			validation.PermissionsPreapproved = true
			req, err = BuildShellRequest(args, e.sessionShell(), validation)
			if err != nil {
				return nil, RespondToModel(err.Error())
			}
		} else {
			body := shellApprovalRequiredMessage(req)
			return &Output{
				Success:    false,
				Body:       body,
				Error:      body,
				Data:       shellApprovalRequestData(req),
				LogPreview: shellLogPreview(body),
			}, nil
		}
	}
	result, err := e.shellRunner().Run(ctx, req)
	if err != nil {
		return nil, err
	}
	body := ShellResultModelText(result, req.MaxOutputTokens)
	return &Output{
		Success:    true,
		Body:       body,
		Data:       shellResultData(result, req.MaxOutputTokens),
		LogPreview: shellLogPreview(body),
	}, nil
}

func (e *ShellExecutor) PreToolUsePayload(invocation *Invocation) (*PreToolUsePayload, bool) {
	args, err := decodeExecCommandInvocation(invocation)
	if err != nil {
		return nil, false
	}
	return &PreToolUsePayload{
		ToolName:  bashHookToolName(),
		ToolInput: shellHookInput(args),
	}, true
}

func (e *ShellExecutor) PostToolUsePayload(invocation *Invocation, output *Output) (*PostToolUsePayload, bool) {
	args, err := decodeExecCommandInvocation(invocation)
	if err != nil || strings.TrimSpace(args.Cmd) == "" {
		return nil, false
	}
	if output == nil {
		return nil, false
	}
	response, ok := output.Data["hook_response"].(string)
	if !ok {
		response = output.Body
	}
	if strings.TrimSpace(response) == "" && output.Data == nil {
		return nil, false
	}
	return &PostToolUsePayload{
		ToolName:     bashHookToolName(),
		ToolUseID:    firstNonEmptyString(output.CallID, invocation.CallID),
		ToolInput:    shellHookInput(args),
		ToolResponse: response,
	}, true
}

func (e *ShellExecutor) WithUpdatedHookInput(invocation *Invocation, updatedInput any) (*Invocation, error) {
	command, err := updatedHookCommand(updatedInput)
	if err != nil {
		return nil, err
	}
	var args map[string]any
	if invocation == nil || invocation.Payload.Kind != PayloadFunction {
		return nil, RespondToModel("hook input rewrite received unsupported exec_command payload")
	}
	if strings.TrimSpace(invocation.Payload.Arguments) == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(invocation.Payload.Arguments), &args); err != nil {
		return nil, RespondToModel(fmt.Sprintf("failed to parse exec_command arguments for hook rewrite: %v", err))
	}
	args["cmd"] = command
	data, err := json.Marshal(args)
	if err != nil {
		return nil, RespondToModel(fmt.Sprintf("failed to serialize rewritten exec_command arguments: %v", err))
	}
	updated := cloneInvocation(invocation)
	updated.Payload.Arguments = string(data)
	return updated, nil
}

func ShellResultModelText(result *ShellResult, maxOutputTokens *int) string {
	if result == nil {
		return ""
	}
	hookResponse := ShellResultHookResponse(result, maxOutputTokens)
	sections := []string{
		fmt.Sprintf("Wall time: %.4f seconds", result.Duration.Seconds()),
	}
	if result.TimedOut {
		sections = append(sections, "Process timed out")
	} else {
		sections = append(sections, fmt.Sprintf("Process exited with code %d", result.ExitCode))
	}
	sections = append(sections, "Output:", hookResponse)
	return strings.Join(sections, "\n")
}

func ShellResultHookResponse(result *ShellResult, maxOutputTokens *int) string {
	response := shellResultHookResponse(result, maxOutputTokens)
	return response.Text
}

type shellTruncatedText struct {
	Text               string
	Truncated          bool
	OriginalTokenCount int
	TotalLines         int
}

func shellResultHookResponse(result *ShellResult, maxOutputTokens *int) *shellTruncatedText {
	if result == nil {
		return &shellTruncatedText{}
	}
	output := shellOutputText(result)
	maxTokens := DefaultExecCommandMaxOutputTokens
	if maxOutputTokens != nil {
		maxTokens = *maxOutputTokens
	}
	return truncateShellText(output, maxTokens)
}

func decodeExecCommandInvocation(invocation *Invocation) (*ExecCommandArgs, error) {
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	var args ExecCommandArgs
	if err := invocation.DecodeArguments(&args); err != nil {
		return nil, err
	}
	return &args, nil
}

func (e *ShellExecutor) sessionShell() *Shell {
	if e == nil || e.shell == nil {
		return NewDefaultShell()
	}
	return e.shell
}

func (e *ShellExecutor) shellRunner() ShellRunner {
	if e == nil || e.runner == nil {
		return NewLocalShellRunner()
	}
	return e.runner
}

func (e *ShellExecutor) validationOptions() ShellValidationOptions {
	if e == nil {
		return ShellValidationOptions{ApprovalPolicy: sandbox.ApprovalOnRequest}
	}
	opts := e.validation
	if opts.ApprovalPolicy == "" {
		opts.ApprovalPolicy = sandbox.ApprovalOnRequest
	}
	return opts
}

func bashHookToolName() *HookToolName {
	return &HookToolName{Name: "Bash"}
}

func shellHookInput(args *ExecCommandArgs) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	input := map[string]any{"command": args.Cmd}
	if strings.TrimSpace(args.CWD) != "" {
		input["cwd"] = args.CWD
	}
	if strings.TrimSpace(args.Workdir) != "" {
		input["workdir"] = args.Workdir
	}
	if len(args.Env) > 0 {
		input["env"] = cloneEnv(args.Env)
	}
	if args.TimeoutMS != 0 {
		input["timeout_ms"] = args.TimeoutMS
	}
	return input
}

func shellResultData(result *ShellResult, maxOutputTokens *int) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	maxTokens := DefaultExecCommandMaxOutputTokens
	if maxOutputTokens != nil {
		maxTokens = *maxOutputTokens
	}
	stdout := truncateShellText(result.Stdout, maxTokens)
	stderr := truncateShellText(result.Stderr, maxTokens)
	hookResponse := shellResultHookResponse(result, maxOutputTokens)
	return map[string]any{
		"stdout":                  result.Stdout,
		"stderr":                  result.Stderr,
		"stdout_truncated":        stdout.Truncated,
		"stderr_truncated":        stderr.Truncated,
		"exit_code":               result.ExitCode,
		"duration_ms":             result.Duration.Milliseconds(),
		"timed_out":               result.TimedOut,
		"hook_response":           hookResponse.Text,
		"hook_response_truncated": hookResponse.Truncated,
	}
}

func shellApprovalRequiredMessage(req *ShellRequest) string {
	if req == nil {
		return "Approval required before running command."
	}
	reason := strings.TrimSpace(req.ApprovalReason)
	if reason == "" {
		reason = "command requested sandbox permissions"
	}
	return fmt.Sprintf("Approval required before running command: %s", reason)
}

func shellApprovalRequestData(req *ShellRequest) map[string]any {
	data := map[string]any{
		"approval_required": true,
		"retry_context":     map[string]any{"permissions_preapproved": true},
	}
	if req == nil {
		return data
	}
	data["command"] = cloneStrings(req.Command)
	data["hook_command"] = req.HookCommand
	data["cwd"] = req.CWD
	data["sandbox_permissions"] = req.SandboxPermissions
	data["reason"] = req.ApprovalReason
	data["justification"] = req.Justification
	if len(req.PrefixRule) > 0 {
		data["prefix_rule"] = cloneStrings(req.PrefixRule)
	}
	if req.AdditionalPermissions != nil {
		data["additional_permissions"] = req.AdditionalPermissions
	}
	if req.SandboxProfile != nil {
		data["sandbox_profile"] = req.SandboxProfile
	}
	return data
}

func shellApprovalDeniedData(req *ShellRequest, decision string) map[string]any {
	data := shellApprovalRequestData(req)
	data["approval_required"] = false
	data["approval_decision"] = decision
	return data
}

func invocationPermissionPreapproved(invocation *Invocation) bool {
	if invocation == nil || invocation.Context == nil {
		return false
	}
	if value, ok := invocation.Context["permissions_preapproved"].(bool); ok && value {
		return true
	}
	if value, ok := invocation.Context["approval_preapproved"].(bool); ok && value {
		return true
	}
	retryContext, ok := invocation.Context["retry_context"].(map[string]any)
	if !ok {
		return false
	}
	value, _ := retryContext["permissions_preapproved"].(bool)
	return value
}

func shellOutputText(result *ShellResult) string {
	if result == nil {
		return ""
	}
	if result.Stderr == "" {
		return result.Stdout
	}
	if result.Stdout == "" {
		return result.Stderr
	}
	return strings.TrimRight(result.Stdout, "\r\n") + "\n\nstderr:\n" + result.Stderr
}

func truncateShellText(content string, maxTokens int) *shellTruncatedText {
	if content == "" {
		return &shellTruncatedText{}
	}
	policy := utils.TokensPolicy(maxTokens)
	if len(content) <= (&policy).ByteBudget() {
		return &shellTruncatedText{Text: content, TotalLines: shellOutputLineCount(content)}
	}
	return &shellTruncatedText{
		Text:               utils.FormattedTruncateText(content, policy),
		Truncated:          true,
		OriginalTokenCount: utils.ApproxTokenCount(content),
		TotalLines:         shellOutputLineCount(content),
	}
}

func shellOutputLineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func shellLogPreview(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= 200 {
		return body
	}
	return body[:200]
}

func updatedHookCommand(updatedInput any) (string, error) {
	value, ok := updatedInput.(map[string]any)
	if !ok {
		return "", RespondToModel("hook returned updatedInput without string field `command`")
	}
	command, ok := value["command"].(string)
	if !ok {
		return "", RespondToModel("hook returned updatedInput without string field `command`")
	}
	return command, nil
}

var _ Executor = (*ShellExecutor)(nil)
var _ PreToolUsePayloadProvider = (*ShellExecutor)(nil)
var _ PostToolUsePayloadProvider = (*ShellExecutor)(nil)
var _ HookInputUpdater = (*ShellExecutor)(nil)
