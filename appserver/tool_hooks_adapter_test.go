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
