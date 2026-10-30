package state

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #57758: a write_stdin Guardian review names the terminal's
// launch command, while a plain exec_command review has no such field.
func TestGuardianWriteStdinReviewIncludesOriginalCommandLikeRust(t *testing.T) {
	tty := true
	action := Action{
		Type:                  "write_stdin",
		EnvironmentID:         "windows-executor",
		SessionID:             intPtrForTest(1000),
		Chars:                 "y\n",
		CWD:                   `C:\workspace`,
		SandboxPermissions:    "with_additional_permissions",
		TTY:                   &tty,
		OriginalCommand:       "python helper.py --project 'example site'",
		ApprovalID:            "terminal-write",
		ProcessID:             "1000",
		AdditionalPermissions: map[string]any{"network": true},
	}
	pretty, truncated, err := FormatGuardianActionPretty(action)
	if err != nil || truncated {
		t.Fatalf("format action: %v (truncated=%v)", err, truncated)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(pretty), &decoded); err != nil {
		t.Fatalf("decode action: %v\n%s", err, pretty)
	}
	if decoded["original_command"] != action.OriginalCommand {
		t.Fatalf("original_command = %#v, want %q", decoded["original_command"], action.OriginalCommand)
	}
	if decoded["tool"] != "write_stdin" || decoded["chars"] != "y\n" {
		t.Fatalf("write_stdin action = %#v", decoded)
	}

	execAction := Action{Type: "execve", Command: "ls", CommandArgv: []string{"ls"}, CWD: "/workspace"}
	pretty, _, err = FormatGuardianActionPretty(execAction)
	if err != nil {
		t.Fatalf("format exec action: %v", err)
	}
	if strings.Contains(pretty, "original_command") {
		t.Fatalf("exec_command review must not carry original_command:\n%s", pretty)
	}
}

func intPtrForTest(value int) *int { return &value }
