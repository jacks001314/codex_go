package appserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func hookInputCWDValue(t *testing.T, request any) string {
	t.Helper()
	raw, err := HookInputJSON(request)
	if err != nil {
		t.Fatalf("HookInputJSON() error = %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal hook input: %v", err)
	}
	value, _ := input["cwd"].(string)
	return value
}

// Rust #57198/#57203: a local command's execution directory replaces the host
// cwd in PreToolUse, PermissionRequest, and PostToolUse hook input. A missing or
// empty tool cwd keeps the host directory used to launch the hook process.
func TestHookInputCWDReportsToolExecutionDirectoryLikeRust(t *testing.T) {
	hostCWD := filepath.Join(t.TempDir(), "host")
	toolCWD := filepath.Join(t.TempDir(), "work")
	empty := ""
	cases := []struct {
		event string
		build func(toolCWD *string) any
	}{
		{
			event: "PreToolUse",
			build: func(toolCWD *string) any {
				return &HookPreToolUseRequest{ThreadID: "thread-1", CWD: hostCWD, ToolCWD: toolCWD, ToolName: "Bash", ToolUseID: "call-1"}
			},
		},
		{
			event: "PermissionRequest",
			build: func(toolCWD *string) any {
				return &HookPermissionRequestRequest{ThreadID: "thread-1", CWD: hostCWD, ToolCWD: toolCWD, ToolName: "Bash"}
			},
		},
		{
			event: "PostToolUse",
			build: func(toolCWD *string) any {
				return &HookPostToolUseRequest{ThreadID: "thread-1", CWD: hostCWD, ToolCWD: toolCWD, ToolName: "Bash", ToolUseID: "call-1"}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			if got := hookInputCWDValue(t, tc.build(&toolCWD)); got != toolCWD {
				t.Fatalf("cwd = %q, want tool cwd %q", got, toolCWD)
			}
			if got := hookInputCWDValue(t, tc.build(nil)); got != hostCWD {
				t.Fatalf("cwd = %q, want host cwd %q", got, hostCWD)
			}
			if got := hookInputCWDValue(t, tc.build(&empty)); got != hostCWD {
				t.Fatalf("cwd = %q, want host cwd %q for empty tool cwd", got, hostCWD)
			}
		})
	}
}
