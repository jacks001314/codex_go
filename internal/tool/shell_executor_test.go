package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/internal/sandbox"
)

func TestShellExecutorRunsCommandAndFormatsOutput(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{
		ExitCode: 7,
		Stdout:   "out\n",
		Stderr:   "err\n",
		Duration: 1500 * time.Millisecond,
	}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			AdditionalPermissionsAllowed: true,
			ApprovalPolicy:               sandbox.ApprovalOnRequest,
			CWD:                          t.TempDir(),
			DefaultTimeoutMS:             5000,
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"printf hi","max_output_tokens":100}`},
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success {
		t.Fatalf("Success = false")
	}
	if runner.request == nil || runner.request.HookCommand != "printf hi" || runner.request.TimeoutMS != 5000 {
		t.Fatalf("request = %#v", runner.request)
	}
	if !strings.Contains(output.Body, "Process exited with code 7") || !strings.Contains(output.Body, "out\n\nstderr:\nerr\n") {
		t.Fatalf("Body = %q", output.Body)
	}
	if output.Data["exit_code"] != 7 || output.Data["hook_response"] != "out\n\nstderr:\nerr\n" {
		t.Fatalf("Data = %#v", output.Data)
	}
}

func TestShellExecutorMarksTruncatedOutput(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{
		ExitCode: 0,
		Stdout:   strings.Repeat("stdout line\n", 20),
		Stderr:   strings.Repeat("stderr line\n", 20),
		Duration: time.Second,
	}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:   sandbox.ApprovalOnRequest,
			CWD:              t.TempDir(),
			DefaultTimeoutMS: 5000,
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-truncate",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"printf hi","max_output_tokens":5}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Data["hook_response_truncated"] != true {
		t.Fatalf("Data = %#v", output.Data)
	}
	if !strings.Contains(output.Body, "Warning: truncated output") {
		t.Fatalf("Body = %q", output.Body)
	}
}

func TestShellResultHookResponseSeparatesStderr(t *testing.T) {
	stdoutOnly := ShellResultHookResponse(&ShellResult{Stdout: "out\n"}, nil)
	if stdoutOnly != "out\n" {
		t.Fatalf("stdoutOnly = %q", stdoutOnly)
	}
	withStderr := ShellResultHookResponse(&ShellResult{Stdout: "out\n", Stderr: "err\n"}, nil)
	if withStderr != "out\n\nstderr:\nerr\n" {
		t.Fatalf("withStderr = %q", withStderr)
	}
}

func TestShellExecutorSpecIncludesRuntimeFields(t *testing.T) {
	spec := NewShellExecutor(&ShellExecutorOptions{}).Spec()
	properties, ok := spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", spec.InputSchema["properties"])
	}
	for _, name := range []string{"cwd", "workdir", "env", "timeout_ms"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("property %q missing from schema %#v", name, properties)
		}
	}
}

func TestShellExecutorReturnsApprovalRequestInsteadOfRunning(t *testing.T) {
	runner := &fakeShellRunner{}
	profile := sandbox.WorkspaceWritePermissionProfile()
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			AdditionalPermissionsAllowed: true,
			ApprovalPolicy:               sandbox.ApprovalOnRequest,
			CWD:                          t.TempDir(),
			PermissionProfile:            &profile,
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-approval",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"go test ./...","sandbox_permissions":"require_escalated","justification":"needs network","prefix_rule":["go","test"]}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success {
		t.Fatalf("Success = true")
	}
	if runner.request != nil {
		t.Fatalf("runner should not be called: %#v", runner.request)
	}
	if output.Data["approval_required"] != true || output.Data["sandbox_permissions"] != sandbox.SandboxPermissionsRequireEscalated {
		t.Fatalf("Data = %#v", output.Data)
	}
	retryContext, ok := output.Data["retry_context"].(map[string]any)
	if !ok || retryContext["permissions_preapproved"] != true {
		t.Fatalf("retry_context = %#v", output.Data["retry_context"])
	}
	if _, ok := output.Data["sandbox_profile"].(*ShellSandboxProfile); !ok {
		t.Fatalf("sandbox_profile = %#v", output.Data["sandbox_profile"])
	}
	if !strings.Contains(output.Body, "needs network") {
		t.Fatalf("Body = %q", output.Body)
	}
}

func TestShellExecutorApprovalCallbackApprovesAndRuns(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0, Stdout: "approved\n"}}
	var approvalRequest *ShellApprovalRequest
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			AdditionalPermissionsAllowed: true,
			ApprovalPolicy:               sandbox.ApprovalOnRequest,
			CWD:                          t.TempDir(),
		},
		Approval: func(ctx context.Context, request *ShellApprovalRequest) (ShellApprovalDecision, error) {
			_ = ctx
			approvalRequest = request
			return ShellApprovalDecision{Approved: true, AllowSession: true}, nil
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-callback-approved",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo approved","sandbox_permissions":"require_escalated"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvalRequest == nil || approvalRequest.Request == nil || !approvalRequest.Request.ApprovalRequired {
		t.Fatalf("approvalRequest = %#v", approvalRequest)
	}
	if !output.Success || !strings.Contains(output.Body, "approved") {
		t.Fatalf("output = %#v", output)
	}
	if runner.request == nil || runner.request.ApprovalRequired || runner.request.PermissionProfileID != sandbox.BuiltInPermissionProfileDangerFullAccess {
		t.Fatalf("runner request = %#v", runner.request)
	}
}

func TestShellExecutorApprovalCallbackDeniesWithoutRunning(t *testing.T) {
	runner := &fakeShellRunner{}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			AdditionalPermissionsAllowed: true,
			ApprovalPolicy:               sandbox.ApprovalOnRequest,
			CWD:                          t.TempDir(),
		},
		Approval: func(ctx context.Context, request *ShellApprovalRequest) (ShellApprovalDecision, error) {
			_ = ctx
			_ = request
			return ShellApprovalDecision{}, nil
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-callback-denied",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo denied","sandbox_permissions":"require_escalated"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success || output.Data["approval_decision"] != "deny" {
		t.Fatalf("output = %#v", output)
	}
	if runner.request != nil {
		t.Fatalf("runner should not be called: %#v", runner.request)
	}
}

func TestShellExecutorRunsApprovedRetryFromInvocationContext(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0, Stdout: "approved\n"}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			AdditionalPermissionsAllowed: true,
			ApprovalPolicy:               sandbox.ApprovalOnRequest,
			CWD:                          t.TempDir(),
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-approved-retry",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo approved","sandbox_permissions":"require_escalated"}`},
		Context:  map[string]any{"retry_context": map[string]any{"permissions_preapproved": true}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || runner.request == nil {
		t.Fatalf("output=%#v request=%#v", output, runner.request)
	}
	if runner.request.ApprovalRequired {
		t.Fatalf("ApprovalRequired = true")
	}
}

func TestShellExecutorRunsPreapprovedSandboxOverride(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0, Stdout: "ok\n"}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			AdditionalPermissionsAllowed: true,
			ApprovalPolicy:               sandbox.ApprovalOnRequest,
			PermissionsPreapproved:       true,
			CWD:                          t.TempDir(),
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-preapproved",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo ok","sandbox_permissions":"require_escalated"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || runner.request == nil {
		t.Fatalf("output=%#v request=%#v", output, runner.request)
	}
	if runner.request.ApprovalRequired {
		t.Fatalf("ApprovalRequired = true")
	}
}

func TestShellExecutorPrePostHookPayload(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{
		Validation: ShellValidationOptions{CWD: t.TempDir(), ApprovalPolicy: sandbox.ApprovalOnRequest},
	})
	invocation := &Invocation{
		CallID:   "call-2",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo before","cwd":"src","env":{"A":"B"},"timeout_ms":77}`},
	}

	pre, ok := executor.PreToolUsePayload(invocation)
	if !ok {
		t.Fatal("PreToolUsePayload() ok = false")
	}
	if pre.ToolName == nil || pre.ToolName.Name != "Bash" {
		t.Fatalf("pre.ToolName = %#v", pre.ToolName)
	}
	if input, ok := pre.ToolInput.(map[string]any); !ok || input["command"] != "echo before" || input["cwd"] != "src" || input["timeout_ms"] != uint64(77) {
		t.Fatalf("pre.ToolInput = %#v", pre.ToolInput)
	} else if env, ok := input["env"].(map[string]string); !ok || env["A"] != "B" {
		t.Fatalf("pre.ToolInput env = %#v", input["env"])
	}

	post, ok := executor.PostToolUsePayload(invocation, &Output{
		CallID: "event-call",
		Body:   "model body",
		Data:   map[string]any{"hook_response": "hook output"},
	})
	if !ok {
		t.Fatal("PostToolUsePayload() ok = false")
	}
	if post.ToolName == nil || post.ToolName.Name != "Bash" || post.ToolUseID != "event-call" {
		t.Fatalf("post = %#v", post)
	}
	if input, ok := post.ToolInput.(map[string]any); !ok || input["command"] != "echo before" || input["cwd"] != "src" {
		t.Fatalf("post.ToolInput = %#v", post.ToolInput)
	}
	if post.ToolResponse != "hook output" {
		t.Fatalf("ToolResponse = %#v", post.ToolResponse)
	}
}

func TestShellExecutorUpdatedHookInputRewritesCmd(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{})
	updated, err := executor.WithUpdatedHookInput(&Invocation{
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo old","yield_time_ms":10}`},
	}, map[string]any{"command": "echo new"})
	if err != nil {
		t.Fatalf("WithUpdatedHookInput() error = %v", err)
	}
	var args ExecCommandArgs
	if err := json.Unmarshal([]byte(updated.Payload.Arguments), &args); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if args.Cmd != "echo new" || args.YieldTimeMS != 10 {
		t.Fatalf("args = %#v", args)
	}

	_, err = executor.WithUpdatedHookInput(&Invocation{
		Payload: Payload{Kind: PayloadFunction, Arguments: `{}`},
	}, map[string]any{"cmd": "wrong"})
	if err == nil || !strings.Contains(err.Error(), "string field `command`") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegisterShellHandler(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterShellHandler(registry, &ShellExecutorOptions{
		Runner: &fakeShellRunner{},
		Validation: ShellValidationOptions{
			CWD:            t.TempDir(),
			ApprovalPolicy: sandbox.ApprovalOnRequest,
		},
	}); err != nil {
		t.Fatalf("RegisterShellHandler() error = %v", err)
	}
	if _, ok := registry.Lookup(PlainName(DefaultExecCommandToolName)); !ok {
		t.Fatal("exec_command handler not registered")
	}
}

func TestExecCommandArgsDecodeSnakeCasePermissions(t *testing.T) {
	var args ExecCommandArgs
	err := json.Unmarshal([]byte(`{
		"cmd": "echo hi",
		"yield_time_ms": 12,
		"max_output_tokens": 34,
		"sandbox_permissions": "with_additional_permissions",
		"additional_permissions": {
			"network": {"enabled": true},
			"file_system": {"write": ["src"]}
		},
		"cwd": "src",
		"env": {"A": "B"},
		"timeout_ms": 56,
		"prefix_rule": ["go", "list"]
	}`), &args)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if args.YieldTimeMS != 12 || args.MaxOutputTokens == nil || *args.MaxOutputTokens != 34 {
		t.Fatalf("args = %#v", args)
	}
	if args.SandboxPermissions != sandbox.SandboxPermissionsWithAdditionalPermissions {
		t.Fatalf("SandboxPermissions = %q", args.SandboxPermissions)
	}
	if args.AdditionalPermissions == nil || args.AdditionalPermissions.Network == nil || !*args.AdditionalPermissions.Network {
		t.Fatalf("AdditionalPermissions = %#v", args.AdditionalPermissions)
	}
	if len(args.AdditionalPermissions.FileSystem) != 1 || args.AdditionalPermissions.FileSystem[0] != "src" {
		t.Fatalf("FileSystem = %#v", args.AdditionalPermissions.FileSystem)
	}
	if args.CWD != "src" || args.Env["A"] != "B" || args.TimeoutMS != 56 {
		t.Fatalf("runtime args = %#v", args)
	}
	if len(args.PrefixRule) != 2 || args.PrefixRule[0] != "go" {
		t.Fatalf("PrefixRule = %#v", args.PrefixRule)
	}
}

type fakeShellRunner struct {
	request *ShellRequest
	result  *ShellResult
	err     error
}

func (r *fakeShellRunner) Run(ctx context.Context, req *ShellRequest) (*ShellResult, error) {
	_ = ctx
	r.request = req
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &ShellResult{}, nil
}
