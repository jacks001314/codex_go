package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/network"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func TestToolHookAdapterRunsPostToolUseHook(t *testing.T) {
	command := hookRunnerOutputCommand(`{"decision":"block","reason":"review output"}`, "")
	hook := hookRunnerMetadata("post", HookEventPostToolUse, "echo", 0)
	hook.Command = &command
	adapter := NewToolHookAdapter(NewHookRunner(), []HookMetadata{hook}, "thread-1", "turn-1", t.TempDir())
	adapter.Model = "gpt-test"

	outcome, err := adapter.RunPostToolUse(context.Background(), &tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.PlainName("echo"),
	}, &tool.PostToolUsePayload{
		ToolName:     &tool.HookToolName{Name: "echo"},
		ToolUseID:    "call-1",
		ToolInput:    map[string]any{},
		ToolResponse: "original",
	})
	if err != nil {
		t.Fatalf("RunPostToolUse() error = %v", err)
	}
	if outcome == nil || !outcome.Blocked || outcome.FeedbackMessage != "review output" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// Rust reports the issuing settings to the pre/post tool-use hooks
// (hook_runtime::run_pre_tool_use_hooks uses step_context.settings and
// hook_permission_mode of the effective approval policy). Go resolves those
// settings for the turn: the request's model, else the configured model, and the
// effective approval policy (a never policy reports bypassPermissions).
func TestTurnHookAdapterReportsCapturedModelAndPermissionModeLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	configBody := "model = \"gpt-5.4\"\napproval_policy = \"never\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	captured := filepath.Join(t.TempDir(), "pre-tool-use.json")
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	hooksJSON, err := json.Marshal(map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "echo",
				"hooks":   []any{map[string]any{"type": "command", "command": captureHookStdinCommand(captured)}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Marshal hooks error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
		t.Fatalf("WriteFile hooks error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Config:         config.NewConfigService(home),
		HooksDiscovery: NewHookDiscoveryService(home),
		HookRunner:     NewHookRunner(),
	})

	adapter, ok := router.turnHookAdapter(&turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd}, "turn-1").(*ToolHookAdapter)
	if !ok || adapter == nil {
		t.Fatalf("adapter = %#v", adapter)
	}
	if adapter.Model != "gpt-5.4" || adapter.PermissionMode != "bypassPermissions" {
		t.Fatalf("adapter model = %q permission mode = %q, want the configured model and bypassPermissions", adapter.Model, adapter.PermissionMode)
	}
	if _, err := adapter.RunPreToolUse(context.Background(), &tool.Invocation{
		CallID:   "call-1",
		ToolName: tool.PlainName("echo"),
	}, &tool.PreToolUsePayload{
		ToolName:  &tool.HookToolName{Name: "echo"},
		ToolInput: map[string]any{"command": "echo hi"},
	}); err != nil {
		t.Fatalf("RunPreToolUse() error = %v", err)
	}
	inputJSON, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("the hook did not receive its stdin payload: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		t.Fatalf("hook input json error = %v payload=%s", err, inputJSON)
	}
	if input["model"] != "gpt-5.4" || input["permission_mode"] != "bypassPermissions" {
		t.Fatalf("hook input attribution = %#v", input)
	}
	if input["tool_name"] != "echo" || input["tool_use_id"] != "call-1" {
		t.Fatalf("hook input tool fields = %#v", input)
	}
}

// Rust's run_pre_tool_use_hooks reports the step's model; a turn that overrides
// the configured model reports the override.
func TestTurnHookAdapterPrefersTheTurnModelOverTheConfiguredModel(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5.4\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD: cwd,
		Config:     config.NewConfigService(home),
		HookRunner: NewHookRunner(),
	})
	adapter, ok := router.turnHookAdapter(&turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4-mini"}, "turn-1").(*ToolHookAdapter)
	if !ok || adapter == nil {
		t.Fatalf("adapter = %#v", adapter)
	}
	if adapter.Model != "gpt-5.4-mini" || adapter.PermissionMode != "default" {
		t.Fatalf("adapter model = %q permission mode = %q, want the turn model and the default permission mode", adapter.Model, adapter.PermissionMode)
	}
}

func captureHookStdinCommand(path string) string {
	if runtime.GOOS == "windows" {
		return powershellEncodedCommand(`[Console]::In.ReadToEnd() | Set-Content -Path ` + powerShellSingleQuote(path))
	}
	return "cat > " + shellQuote(path)
}

// Rust Session::request_approval runs PermissionRequest hooks before the user
// approval request: an allow approves the command, a deny rejects it with the
// hook's message, and a hook that declines to decide changes nothing.
func TestShellApprovalRunsPermissionRequestHooksLikeRust(t *testing.T) {
	run := func(t *testing.T, hookCommand string) tool.ShellApprovalDecision {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
		hooksDir := filepath.Join(cwd, ".gcode")
		if err := os.MkdirAll(hooksDir, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		hooksJSON, err := json.Marshal(map[string]any{
			"hooks": map[string]any{
				"PermissionRequest": []any{map[string]any{
					"matcher": "Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": hookCommand}},
				}},
			},
		})
		if err != nil {
			t.Fatalf("Marshal hooks error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
			t.Fatalf("WriteFile hooks error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:     cwd,
			Config:         config.NewConfigService(home),
			HooksDiscovery: NewHookDiscoveryService(home),
			HookRunner:     NewHookRunner(),
		})
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		approval := router.shellApprovalForTurn("thread-1", "turn-1", false)
		decision, err := approval(context.Background(), &tool.ShellApprovalRequest{
			Request:    &tool.ShellRequest{HookCommand: "rm -rf build", CWD: cwd, Justification: "clean the build"},
			Invocation: &tool.Invocation{CallID: "call-1"},
		})
		if err != nil {
			t.Fatalf("shell approval error = %v", err)
		}
		return decision
	}

	allow := run(t, hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`, ""))
	if !allow.Approved {
		t.Fatalf("hook allow did not approve the command: %#v", allow)
	}

	deny := run(t, hookRunnerPermissionRequestDenyCommand("blocked by policy"))
	if deny.Approved {
		t.Fatalf("hook deny approved the command: %#v", deny)
	}
	if deny.DenyReason != "blocked by policy" {
		t.Fatalf("hook deny reason = %q, want %q", deny.DenyReason, "blocked by policy")
	}
}

// A PermissionRequest hook that prints an explicit allow reaches the approval
// path's payload: the hook stdin carries the command and justification under
// Rust's Bash tool name.
func TestShellApprovalPermissionRequestHookPayloadLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
	configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	captured := filepath.Join(t.TempDir(), "permission-request.json")
	hooksDir := filepath.Join(cwd, ".gcode")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	hooksJSON, err := json.Marshal(map[string]any{
		"hooks": map[string]any{
			"PermissionRequest": []any{map[string]any{
				"matcher": "Bash",
				"hooks":   []any{map[string]any{"type": "command", "command": captureHookStdinCommand(captured)}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Marshal hooks error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
		t.Fatalf("WriteFile hooks error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     cwd,
		Config:         config.NewConfigService(home),
		HooksDiscovery: NewHookDiscoveryService(home),
		HookRunner:     NewHookRunner(),
	})
	params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	// The capturing hook prints nothing, so the hooks decline to decide and the
	// approval path proceeds to its next stage; only the payload is asserted.
	_, _ = router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), &tool.ShellApprovalRequest{
		Request:    &tool.ShellRequest{HookCommand: "rm -rf build", CWD: cwd, Justification: "clean the build"},
		Invocation: &tool.Invocation{CallID: "call-1"},
	})
	payload, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("the hook did not receive its stdin payload: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal(payload, &input); err != nil {
		t.Fatalf("hook input json error = %v payload=%s", err, payload)
	}
	if input["tool_name"] != "Bash" {
		t.Fatalf("permission request tool_name = %#v", input["tool_name"])
	}
	toolInput, _ := input["tool_input"].(map[string]any)
	if toolInput["command"] != "rm -rf build" || toolInput["description"] != "clean the build" {
		t.Fatalf("permission request tool_input = %#v", input["tool_input"])
	}
	if input["permission_mode"] != "default" || input["model"] != "gpt-5.4" {
		t.Fatalf("permission request attribution = %#v", input)
	}
}

// Rust's ApplyPatch permission-request payload reports the canonical
// apply_patch tool name with the Write/Edit matcher aliases and the patch body
// as `command`, and its verdict decides the patch approval.
func TestApplyPatchApprovalRunsPermissionRequestHooksLikeRust(t *testing.T) {
	buildRouter := func(t *testing.T, matcher string, hookCommand string) *RuntimeRouter {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
		hooksDir := filepath.Join(cwd, ".gcode")
		if err := os.MkdirAll(hooksDir, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		hooksJSON, err := json.Marshal(map[string]any{
			"hooks": map[string]any{
				"PermissionRequest": []any{map[string]any{
					"matcher": matcher,
					"hooks":   []any{map[string]any{"type": "command", "command": hookCommand}},
				}},
			},
		})
		if err != nil {
			t.Fatalf("Marshal hooks error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
			t.Fatalf("WriteFile hooks error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:     cwd,
			Config:         config.NewConfigService(home),
			HooksDiscovery: NewHookDiscoveryService(home),
			HookRunner:     NewHookRunner(),
		})
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		return router
	}
	approve := func(t *testing.T, router *RuntimeRouter) (tool.ApplyPatchApprovalDecision, error) {
		t.Helper()
		return router.applyPatchApprovalForTurn("thread-1", "turn-1")(context.Background(), &tool.ApplyPatchApprovalRequest{
			Patch:      "*** Begin Patch\n*** End Patch\n",
			CWD:        t.TempDir(),
			Invocation: &tool.Invocation{CallID: "call-1"},
		})
	}

	allowed, err := approve(t, buildRouter(t, "apply_patch", hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`, "")))
	if err != nil {
		t.Fatalf("patch approval error = %v", err)
	}
	if !allowed.Approved {
		t.Fatalf("hook allow did not approve the patch: %#v", allowed)
	}
	denied, err := approve(t, buildRouter(t, "apply_patch", hookRunnerPermissionRequestDenyCommand("no patch today")))
	if err != nil {
		t.Fatalf("patch approval error = %v", err)
	}
	if denied.Approved || denied.DenyReason != "no patch today" {
		t.Fatalf("hook deny = %#v", denied)
	}

	// The Write matcher alias selects the handler, and the hook input keeps the
	// canonical apply_patch tool name with the patch body as `command`.
	captured := filepath.Join(t.TempDir(), "permission-request.json")
	router := buildRouter(t, "Write", captureHookStdinCommand(captured))
	// The capturing hook declines to decide, so the approval path continues to
	// the user request, which this runtime has no sink for; only the payload the
	// hook received is asserted.
	_, _ = approve(t, router)
	payload, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("the hook did not receive its stdin payload: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal(payload, &input); err != nil {
		t.Fatalf("hook input json error = %v payload=%s", err, payload)
	}
	if input["tool_name"] != "apply_patch" {
		t.Fatalf("permission request tool_name = %#v", input["tool_name"])
	}
	toolInput, _ := input["tool_input"].(map[string]any)
	if toolInput["command"] != "*** Begin Patch\n*** End Patch\n" {
		t.Fatalf("permission request tool_input = %#v", input["tool_input"])
	}
}

// fakeGuardianReviewer records the actions an approval path submits for review
// and returns a canned verdict.
type fakeGuardianReviewer struct {
	decision state.ReviewDecision
	reason   string
	err      error
	actions  []state.Action
}

func (f *fakeGuardianReviewer) Review(ctx context.Context, threadID string, turnID string, targetItemID string, action state.Action) (state.ReviewDecision, string, error) {
	f.actions = append(f.actions, action)
	return f.decision, f.reason, f.err
}

// Rust Session::request_approval: an auto-review turn routes command and patch
// approvals through the Guardian review instead of the user approval request.
func TestAutoReviewApprovalsRouteThroughGuardianLikeRust(t *testing.T) {
	newRouter := func(t *testing.T, reviewer GuardianReviewer) *RuntimeRouter {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		configBody := "model = \"gpt-5.4\"\napprovals_reviewer = \"auto_review\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:       cwd,
			Config:           config.NewConfigService(home),
			GuardianReviewer: reviewer,
		})
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		return router
	}
	shellRequest := func(t *testing.T) *tool.ShellApprovalRequest {
		t.Helper()
		return &tool.ShellApprovalRequest{
			Request:    &tool.ShellRequest{HookCommand: "rm -rf build", CWD: t.TempDir(), Justification: "clean the build"},
			Invocation: &tool.Invocation{CallID: "call-1"},
		}
	}

	approved := &fakeGuardianReviewer{decision: state.DecisionApproved}
	decision, err := newRouter(t, approved).shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), shellRequest(t))
	if err != nil {
		t.Fatalf("shell approval error = %v", err)
	}
	if !decision.Approved {
		t.Fatalf("guardian approval = %#v", decision)
	}
	if len(approved.actions) != 1 {
		t.Fatalf("guardian actions = %#v", approved.actions)
	}
	action := approved.actions[0]
	// Rust's ExecCommand carries the approval request's reason for the framing
	// and the command's own justification in the action JSON.
	if action.Type != "command" || action.Command != "rm -rf build" || action.Reason != "" ||
		action.Justification != "clean the build" || action.Source != state.CommandSourceShell {
		t.Fatalf("guardian command action = %#v", action)
	}

	denied := &fakeGuardianReviewer{decision: state.DecisionDenied, reason: "too destructive"}
	decision, err = newRouter(t, denied).shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), shellRequest(t))
	if err != nil {
		t.Fatalf("shell approval error = %v", err)
	}
	if decision.Approved || decision.DenyReason != "too destructive" {
		t.Fatalf("guardian denial = %#v", decision)
	}

	aborted := &fakeGuardianReviewer{decision: state.DecisionAborted, reason: "turn aborted"}
	if _, err := newRouter(t, aborted).shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), shellRequest(t)); err == nil ||
		!strings.Contains(err.Error(), "turn aborted") {
		t.Fatalf("guardian abort error = %v", err)
	}

	patchReviewer := &fakeGuardianReviewer{decision: state.DecisionApproved}
	patchDecision, err := newRouter(t, patchReviewer).applyPatchApprovalForTurn("thread-1", "turn-1")(context.Background(), &tool.ApplyPatchApprovalRequest{
		Patch:      "*** Begin Patch\n*** End Patch\n",
		CWD:        t.TempDir(),
		Changes:    []map[string]any{{"path": "src/main.go"}},
		Invocation: &tool.Invocation{CallID: "call-2"},
	})
	if err != nil {
		t.Fatalf("patch approval error = %v", err)
	}
	if !patchDecision.Approved {
		t.Fatalf("guardian patch approval = %#v", patchDecision)
	}
	if len(patchReviewer.actions) != 1 {
		t.Fatalf("guardian patch actions = %#v", patchReviewer.actions)
	}
	patchAction := patchReviewer.actions[0]
	if patchAction.Type != "apply_patch" || patchAction.Patch != "*** Begin Patch\n*** End Patch\n" ||
		len(patchAction.Files) != 1 || patchAction.Files[0] != "src/main.go" {
		t.Fatalf("guardian patch action = %#v", patchAction)
	}
}

// Rust runs PermissionRequest hooks before the Guardian review for a
// request_permissions call as well.
func TestRequestPermissionsRunsPermissionRequestHooksLikeRust(t *testing.T) {
	run := func(t *testing.T, hookCommand string) tool.RequestPermissionsDecision {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
		hooksDir := filepath.Join(cwd, ".gcode")
		if err := os.MkdirAll(hooksDir, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		hooksJSON, err := json.Marshal(map[string]any{
			"hooks": map[string]any{
				"PermissionRequest": []any{map[string]any{
					"matcher": "request_permissions",
					"hooks":   []any{map[string]any{"type": "command", "command": hookCommand}},
				}},
			},
		})
		if err != nil {
			t.Fatalf("Marshal hooks error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
			t.Fatalf("WriteFile hooks error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:     cwd,
			Config:         config.NewConfigService(home),
			HooksDiscovery: NewHookDiscoveryService(home),
			HookRunner:     NewHookRunner(),
		})
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		decision, err := router.requestPermissionsGuardianReviewer("thread-1")(
			context.Background(), "", "turn-1", "call-1", "", "need repository write access",
			map[string]any{"write": true},
		)
		if err != nil {
			t.Fatalf("request_permissions review error = %v", err)
		}
		return decision
	}

	allowed := run(t, hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`, ""))
	if !allowed.Approved {
		t.Fatalf("hook allow did not approve request_permissions: %#v", allowed)
	}
	denied := run(t, hookRunnerPermissionRequestDenyCommand("permissions are not granted here"))
	if denied.Approved || denied.Reason != "permissions are not granted here" {
		t.Fatalf("hook deny = %#v", denied)
	}
}

// Rust runs PermissionRequest hooks before the Guardian review and the user
// request for network-access approvals, with the network-access command as the
// Bash hook input.
func TestNetworkApprovalRunsPermissionRequestHooksFirstLikeRust(t *testing.T) {
	run := func(t *testing.T, hookCommand string) network.ProxyDecision {
		t.Helper()
		home := t.TempDir()
		cwd := t.TempDir()
		projectTrust := strings.ReplaceAll(filepath.Clean(cwd), `\`, `\\`)
		configBody := "model = \"gpt-5.4\"\nbypass_hook_trust = true\n[projects.\"" + projectTrust + "\"]\ntrust_level = \"trusted\"\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
			t.Fatalf("WriteFile config error = %v", err)
		}
		hooksDir := filepath.Join(cwd, ".gcode")
		if err := os.MkdirAll(hooksDir, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		hooksJSON, err := json.Marshal(map[string]any{
			"hooks": map[string]any{
				"PermissionRequest": []any{map[string]any{
					"matcher": "Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": hookCommand}},
				}},
			},
		})
		if err != nil {
			t.Fatalf("Marshal hooks error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), hooksJSON, 0o600); err != nil {
			t.Fatalf("WriteFile hooks error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{
			DefaultCWD:     cwd,
			Config:         config.NewConfigService(home),
			HooksDiscovery: NewHookDiscoveryService(home),
			HookRunner:     NewHookRunner(),
		})
		params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
		service := newNetworkApprovalService(router)
		active := &networkApprovalTurn{threadID: "thread-1", turnID: "turn-1", params: params}
		key := networkApprovalKey{threadID: "thread-1", environmentID: "local", protocol: network.ProxyProtocolHTTPSConnect, host: "example.com", port: 443}
		decision, _ := service.requestApproval(context.Background(), active, key, network.ProxyPolicyRequest{
			Protocol: network.ProxyProtocolHTTPSConnect,
			Host:     "example.com",
			Port:     443,
		}, nil)
		return decision
	}

	if decision := run(t, hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`, "")); !decision.Allow {
		t.Fatalf("hook allow did not approve network access: %#v", decision)
	}
	if decision := run(t, hookRunnerPermissionRequestDenyCommand("no network for you")); decision.Allow {
		t.Fatalf("hook deny approved network access: %#v", decision)
	}
}
