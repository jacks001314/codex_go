package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
	"codex_go/network"
	"codex_go/sandbox"
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
	if !regexp.MustCompile(`(?m)^Chunk ID: [0-9a-f]{6}$`).MatchString(output.Body) || !strings.Contains(output.Body, "Original token count:") {
		t.Fatalf("Body metadata = %q", output.Body)
	}
	if output.Data["exit_code"] != 7 || output.Data["hook_response"] != "out\n\nstderr:\nerr\n" {
		t.Fatalf("Data = %#v", output.Data)
	}
	chunkID, _ := output.Data["chunk_id"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{6}$`).MatchString(chunkID) || output.Data["wall_time_seconds"] != 1.5 || output.Data["original_token_count"] == nil || output.Data["output"] != "out\n\nstderr:\nerr\n" {
		t.Fatalf("metadata Data = %#v", output.Data)
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

func TestShellExecutorClampsModelMaxOutputTokensToPolicyLikeRust(t *testing.T) {
	policy := 50
	for _, tc := range []struct {
		name      string
		requested *int
		want      int
	}{
		{name: "uses policy when omitted", want: 50},
		{name: "keeps smaller request", requested: intPtr(5), want: 5},
		{name: "clamps larger request", requested: intPtr(70_000), want: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeShellRunner{result: &ShellResult{Stdout: strings.Repeat("line\n", 100)}}
			executor := NewShellExecutor(&ShellExecutorOptions{
				Runner:          runner,
				Shell:           &Shell{Type: ShellBash, Path: "/bin/sh"},
				MaxOutputTokens: &policy,
				Validation: ShellValidationOptions{
					ApprovalPolicy: sandbox.ApprovalOnRequest,
					CWD:            t.TempDir(),
				},
			})
			arguments := map[string]any{"cmd": "printf hi"}
			if tc.requested != nil {
				arguments["max_output_tokens"] = *tc.requested
			}
			encoded, err := json.Marshal(arguments)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			output, err := executor.Execute(context.Background(), &Invocation{
				ToolName: PlainName(DefaultExecCommandToolName),
				Payload:  Payload{Kind: PayloadFunction, Arguments: string(encoded)},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if runner.request == nil || runner.request.MaxOutputTokens == nil || *runner.request.MaxOutputTokens != tc.want {
				t.Fatalf("request.MaxOutputTokens = %v, want %d", runner.request, tc.want)
			}
			if output == nil || output.Data["hook_response_truncated"] != true {
				t.Fatalf("output = %#v, want truncated hook response", output)
			}
		})
	}
}

func intPtr(value int) *int {
	return &value
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

func TestUnifiedExecSpecsMatchRustSchemas(t *testing.T) {
	manager := NewUnifiedExecManager()
	defer manager.Close()
	execSpec := NewShellExecutor(&ShellExecutorOptions{
		UnifiedExec: manager,
		UnifiedExecEnvironments: []UnifiedExecEnvironment{
			{ID: "primary", CWD: "/primary", Shell: &Shell{Type: ShellBash, Path: "/bin/bash"}},
			{ID: "remote", CWD: "/remote", Shell: &Shell{Type: ShellBash, Path: "/bin/bash"}, ExecServerURL: "ws://example.test"},
		},
		Validation: ShellValidationOptions{
			AllowLoginShell:              true,
			AdditionalPermissionsAllowed: true,
		},
	}).Spec()
	properties, ok := execSpec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("exec properties = %#v", execSpec.InputSchema)
	}
	for _, name := range []string{"cmd", "workdir", "tty", "yield_time_ms", "max_output_tokens", "shell", "login", "environment_id", "sandbox_permissions", "additional_permissions", "justification", "prefix_rule"} {
		if properties[name] == nil {
			t.Fatalf("unified exec property %q missing from %#v", name, properties)
		}
	}
	for _, name := range []string{"cwd", "env", "timeout_ms"} {
		if properties[name] != nil {
			t.Fatalf("Go-only unified exec property %q present in %#v", name, properties)
		}
	}
	if execSpec.InputSchema["additionalProperties"] != false {
		t.Fatalf("exec additionalProperties = %#v", execSpec.InputSchema["additionalProperties"])
	}
	if execSpec.OutputSchema["additionalProperties"] != false {
		t.Fatalf("exec output schema = %#v", execSpec.OutputSchema)
	}

	writeSpec := NewWriteStdinExecutor(manager, nil).Spec()
	writeProperties, ok := writeSpec.InputSchema["properties"].(map[string]any)
	if !ok || writeProperties["session_id"].(map[string]any)["type"] != "number" {
		t.Fatalf("write_stdin properties = %#v", writeSpec.InputSchema)
	}
	if writeSpec.InputSchema["additionalProperties"] != false || writeSpec.OutputSchema["additionalProperties"] != false {
		t.Fatalf("write_stdin schemas = input:%#v output:%#v", writeSpec.InputSchema, writeSpec.OutputSchema)
	}
}

func TestResolveRemoteUnifiedExecCWDUsesEnvironmentPathConventionLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    string
		workdir string
		want    string
	}{
		{name: "posix relative", base: "/workspace/repo", workdir: "sub/../src", want: "/workspace/repo/src"},
		{name: "posix spaces are significant", base: "/workspace/repo", workdir: " child ", want: "/workspace/repo/ child "},
		{name: "posix absolute", base: "/workspace/repo", workdir: "/tmp/build", want: "/tmp/build"},
		{name: "windows relative", base: `C:\workspace\repo`, workdir: `sub\..\src`, want: `C:\workspace\repo\src`},
		{name: "windows absolute", base: `C:\workspace\repo`, workdir: `D:\build`, want: `D:\build`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRemoteUnifiedExecCWD(tc.base, tc.workdir)
			if err != nil || got != tc.want {
				t.Fatalf("resolveRemoteUnifiedExecCWD() = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
	if _, err := resolveRemoteUnifiedExecCWD("/workspace", `C:\build`); err == nil {
		t.Fatal("cross-convention absolute workdir should fail")
	}
}

func TestResolveUnifiedExecEnvironmentMatchesRustWithoutTrimmingID(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{UnifiedExecEnvironments: []UnifiedExecEnvironment{
		{ID: "primary"},
		{ID: "remote"},
	}})
	environment, err := executor.resolveUnifiedExecEnvironment("remote")
	if err != nil || environment == nil || environment.ID != "remote" {
		t.Fatalf("resolve exact id = %#v, %v", environment, err)
	}
	if _, err := executor.resolveUnifiedExecEnvironment(" remote "); err == nil || err.Error() != "unknown turn environment id ` remote `" {
		t.Fatalf("resolve whitespace-padded id error = %v", err)
	}
}

func TestShellExecutorResolvesManagedNetworkForSelectedEnvironmentLikeRust(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0}}
	var resolved []string
	cwd := t.TempDir()
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			CWD:            cwd,
		},
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{ID: "primary", CWD: cwd}, {ID: "remote", CWD: cwd}},
		ManagedNetworkResolver: func(environmentID string) (map[string]string, *network.ProxyManagedNetworkSandboxContext, error) {
			resolved = append(resolved, environmentID)
			port := uint16(41000 + len(resolved))
			return map[string]string{"HTTP_PROXY": "http://127.0.0.1:" + fmt.Sprint(port)}, &network.ProxyManagedNetworkSandboxContext{LoopbackPorts: []uint16{port}}, nil
		},
	})
	for _, arguments := range []string{`{"cmd":"echo primary"}`, `{"cmd":"echo remote","environment_id":"remote"}`} {
		if _, err := executor.Execute(context.Background(), &Invocation{CallID: "call-network", ToolName: PlainName(DefaultExecCommandToolName), Payload: Payload{Kind: PayloadFunction, Arguments: arguments}}); err != nil {
			t.Fatal(err)
		}
		if runner.request == nil || !runner.request.EnforceManagedNetwork || runner.request.ManagedNetwork == nil || len(runner.request.ManagedNetwork.LoopbackPorts) != 1 {
			t.Fatalf("managed network request = %#v", runner.request)
		}
		port := runner.request.ManagedNetwork.LoopbackPorts[0]
		if runner.request.Env["HTTP_PROXY"] != "http://127.0.0.1:"+fmt.Sprint(port) {
			t.Fatalf("managed network env/context mismatch: %#v", runner.request)
		}
	}
	if len(resolved) != 2 || resolved[0] != "primary" || resolved[1] != "remote" {
		t.Fatalf("resolved environment ids = %#v", resolved)
	}
}

func TestShellExecutorUsesUnifiedExecForExplicitlyDisabledSandboxLikeRust(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{UnifiedExec: NewUnifiedExecManager()})
	defer executor.unifiedExec.Close()

	if !executor.shouldUseUnifiedExec(&ShellRequest{}) {
		t.Fatal("nil permission profile should use unified exec")
	}
	if !executor.shouldUseUnifiedExec(&ShellRequest{PermissionProfile: &sandbox.PermissionProfile{Disabled: true}}) {
		t.Fatal("explicitly disabled sandbox should use unified exec")
	}
	provider := execserver.NoiseRendezvousConnectProviderFunc(func(context.Context, execserver.RemotePublicKey) (*execserver.NoiseRendezvousConnectBundle, error) {
		return nil, errors.New("not called")
	})
	if !executor.shouldUseUnifiedExec(&ShellRequest{UnifiedExecNoiseProvider: provider}) {
		t.Fatal("Noise rendezvous environment should use unified exec without a websocket URL")
	}
	usesSandboxedUnifiedExec := executor.shouldUseUnifiedExec(&ShellRequest{PermissionProfile: &sandbox.PermissionProfile{}})
	if usesSandboxedUnifiedExec != (runtime.GOOS == "linux" || runtime.GOOS == "windows") {
		t.Fatalf("active sandbox unified exec = %t on %s", usesSandboxedUnifiedExec, runtime.GOOS)
	}
	profileWithDeniedRead := sandbox.ReadOnlyPermissionProfile()
	profileWithDeniedRead.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{}}
	usesDeniedReadUnifiedExec := executor.shouldUseUnifiedExec(&ShellRequest{PermissionProfile: &profileWithDeniedRead})
	if usesDeniedReadUnifiedExec != (runtime.GOOS == "linux" || runtime.GOOS == "windows") {
		t.Fatalf("deny-read sandbox unified exec = %t on %s", usesDeniedReadUnifiedExec, runtime.GOOS)
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

func TestShellCommandSpecAndArgumentsMatchRustLegacyContract(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{Stdout: "legacy ok\n", ExitCode: 0, HasExitCode: true}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner:     runner,
		ToolName:   PlainName(DefaultShellCommandToolName),
		Validation: ShellValidationOptions{CWD: t.TempDir(), ApprovalPolicy: sandbox.ApprovalNever},
	})
	spec := executor.Spec()
	required, _ := spec.InputSchema["required"].([]string)
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if spec.Name.Key() != DefaultShellCommandToolName || len(required) != 1 || required[0] != "command" {
		t.Fatalf("shell_command spec = %#v", spec)
	}
	if _, ok := properties["command"]; !ok {
		t.Fatalf("shell_command properties = %#v", properties)
	}
	if _, ok := properties["cmd"]; ok {
		t.Fatalf("shell_command exposes exec_command cmd field: %#v", properties)
	}
	if runtime.GOOS == "windows" && (!strings.Contains(spec.Description, "Get-ChildItem -Force") || !strings.Contains(spec.Description, "Start-Process")) {
		t.Fatalf("shell_command Windows guidance = %q", spec.Description)
	}
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "legacy-shell",
		ToolName: PlainName(DefaultShellCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"command":"Write-Output legacy","timeout_ms":1234}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output == nil || runner.request == nil || runner.request.HookCommand != "Write-Output legacy" || runner.request.TimeoutMS != 1234 {
		t.Fatalf("output = %#v, request = %#v", output, runner.request)
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

func TestUnifiedExecSpecWarnsAboutPOSIXHeredocForSelectedPowerShellEnvironment(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{
		UnifiedExec: NewUnifiedExecManager(),
		Shell:       &Shell{Type: ShellZsh, Path: "/bin/zsh"},
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{
			ID:    "windows-vscode",
			Shell: &Shell{Type: ShellPowerShell, Path: "powershell.exe"},
		}},
	})
	description := executor.Spec().Description
	if !strings.Contains(description, "uses PowerShell") || !strings.Contains(description, "python - <<'PY'") {
		t.Fatalf("PowerShell exec description = %q", description)
	}
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
