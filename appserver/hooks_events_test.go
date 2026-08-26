package appserver

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestHookRunnerRunSessionStartBuildsInput(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("session", HookEventSessionStart, "startup", 0)
	command := hookRunnerStdinContainsCommand("hook_event_name", "SessionStart")
	hook.Command = &command

	result, err := runner.RunSessionStart(context.Background(), &HookSessionStartRequest{
		ThreadID:       "thread-1",
		CWD:            t.TempDir(),
		Model:          "gpt-test",
		PermissionMode: "default",
		Source:         SessionStartSourceStartup,
		Hooks:          []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunSessionStart() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
}

func TestHookRunnerRunSessionEndBuildsLifecycleInput(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("session-end", HookEventSessionEnd, "", 0)
	command := hookRunnerStdinContainsCommand("hook_event_name", "SessionEnd", "reason", "archive")
	hook.Command = &command
	result, err := runner.RunSessionEnd(context.Background(), &HookSessionEndRequest{ThreadID: "thread-1", CWD: t.TempDir(), Model: "gpt", PermissionMode: "on-request", Reason: "archive", Hooks: []HookMetadata{hook}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %#v", result)
	}
}

func TestHookRunnerRunInterruptBuildsInput(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("interrupt", HookEventInterrupt, "", 0)
	command := hookRunnerStdinContainsCommand("hook_event_name", "Interrupt", "model", "gpt")
	hook.Command = &command
	result, err := runner.RunInterrupt(context.Background(), &HookInterruptRequest{ThreadID: "thread-1", TurnID: "turn-1", CWD: t.TempDir(), Model: "gpt", PermissionMode: "on-request", Hooks: []HookMetadata{hook}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %#v", result)
	}
}

func TestHookRunnerRunPreToolUseBuildsInputAndRunID(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("pre", HookEventPreToolUse, "Bash|Shell", 0)
	command := hookRunnerStdinContainsCommand("hook_event_name", "PreToolUse", "tool_name", "Bash", "tool_use_id", "call-123")
	hook.Command = &command

	result, err := runner.RunPreToolUse(context.Background(), &HookPreToolUseRequest{
		ThreadID:       "thread-1",
		TurnID:         "turn-1",
		CWD:            t.TempDir(),
		Model:          "gpt-test",
		PermissionMode: "default",
		ToolName:       "Bash",
		MatcherAliases: []string{"Shell"},
		ToolUseID:      "call-123",
		ToolInput:      map[string]any{"command": "echo hi"},
		Hooks:          []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunPreToolUse() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasSuffix(result.Runs[0].ID, ":call-123") {
		t.Fatalf("run id = %q", result.Runs[0].ID)
	}
}

func TestHookRunnerRunPermissionRequestDoesNotExposeToolUseID(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("permission", HookEventPermissionRequest, "Bash", 0)
	command := hookRunnerStdinContainsCommand("hook_event_name", "PermissionRequest", "tool_name", "Bash")
	hook.Command = &command

	result, err := runner.RunPermissionRequest(context.Background(), &HookPermissionRequestRequest{
		ThreadID:       "thread-1",
		TurnID:         "turn-1",
		CWD:            t.TempDir(),
		Model:          "gpt-test",
		PermissionMode: "default",
		ToolName:       "Bash",
		RunIDSuffix:    "approval-1",
		ToolInput:      map[string]any{"command": "echo hi"},
		Hooks:          []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunPermissionRequest() error = %v", err)
	}
	if len(result.Runs) != 1 || !strings.HasSuffix(result.Runs[0].ID, ":approval-1") {
		t.Fatalf("result = %+v", result)
	}
}

func TestHookRunnerRunPreCompactBuildsTriggerInput(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("compact", HookEventPreCompact, "manual", 0)
	command := hookRunnerStdinContainsCommand("hook_event_name", "PreCompact", "trigger", "manual")
	hook.Command = &command

	result, err := runner.RunPreCompact(context.Background(), &HookPreCompactRequest{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		CWD:      t.TempDir(),
		Model:    "gpt-test",
		Trigger:  "manual",
		Hooks:    []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunPreCompact() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
}

func TestHookRunnerRunUserPromptSubmitPlainStdoutAddsContext(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("prompt", HookEventUserPromptSubmit, "", 0)
	command := hookRunnerOutputCommand("remember this prompt note", "")
	hook.Command = &command

	result, err := runner.RunUserPromptSubmit(context.Background(), &HookUserPromptSubmitRequest{
		ThreadID:       "thread-1",
		TurnID:         "turn-1",
		CWD:            t.TempDir(),
		Model:          "gpt-test",
		PermissionMode: "default",
		Prompt:         "hello",
		Hooks:          []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunUserPromptSubmit() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
	if !hookEntriesContain(result.Runs[0].Entries, HookOutputContext, "remember this prompt note") {
		t.Fatalf("entries = %+v", result.Runs[0].Entries)
	}
}

func hookRunnerStdinContainsCommand(parts ...string) string {
	if runtime.GOOS == "windows" {
		conditions := make([]string, 0, len(parts))
		for _, part := range parts {
			conditions = append(conditions, "$data -like "+powerShellSingleQuote("*"+part+"*"))
		}
		return powershellEncodedCommand(`$data = [Console]::In.ReadToEnd(); if (` + strings.Join(conditions, " -and ") + `) { exit 0 } else { exit 9 }`)
	}
	script := "input=$(cat)"
	for _, part := range parts {
		script += "; printf %s \"$input\" | grep -q " + shellQuote(part)
	}
	return script
}
