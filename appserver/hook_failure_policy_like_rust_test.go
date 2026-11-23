package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// Rust #62831: a failed synchronous handler configured with onFailure=block
// applies its event's blocking behavior while preserving the failure diagnostics.
func TestHookFailurePolicyBlocksOnFailureLikeRust(t *testing.T) {
	failing := "exit 3"
	for _, testCase := range []struct {
		name        string
		event       HookEventName
		wantBlocked bool
		wantStopped bool
		wantDeny    bool
	}{
		{name: "pre tool use blocks execution", event: HookEventPreToolUse, wantBlocked: true},
		{name: "post tool use withholds result", event: HookEventPostToolUse, wantBlocked: true},
		{name: "stop blocks completion", event: HookEventStop, wantBlocked: true},
		{name: "user prompt stops the turn", event: HookEventUserPromptSubmit, wantStopped: true},
		{name: "permission request denies", event: HookEventPermissionRequest, wantDeny: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			metadata := hookRunnerMetadata("blocking", testCase.event, "", 0)
			metadata.Command = &failing
			metadata.ExecutionMode = HookExecutionSync
			metadata.OnFailure = HookFailureBlock
			runner := NewHookRunner()
			result, err := runner.Run(context.Background(), &HookRunRequest{
				ThreadID:  "thread-1",
				CWD:       t.TempDir(),
				EventName: testCase.event,
				Hooks:     []HookMetadata{metadata},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(result.Runs) != 1 || result.Runs[0].Status != HookRunBlocked {
				t.Fatalf("run = %#v, want a blocked run", result.Runs)
			}
			reason := hookEventRustName(testCase.event) + " hook failed. This hook is configured to block continuation on failure."
			var feedback bool
			for _, entry := range result.Runs[0].Entries {
				if entry.Kind == HookOutputFeedback && entry.Text == reason {
					feedback = true
				}
			}
			if !feedback {
				t.Fatalf("blocked run lost the failure reason: %#v", result.Runs[0].Entries)
			}
			if result.Blocked != testCase.wantBlocked || result.Stopped != testCase.wantStopped {
				t.Fatalf("blocked=%t stopped=%t, want %t/%t", result.Blocked, result.Stopped, testCase.wantBlocked, testCase.wantStopped)
			}
			if testCase.wantBlocked && (!strings.Contains(result.BlockReason, reason) || !strings.Contains(result.FeedbackMessage, reason)) {
				t.Fatalf("block result = %#v, want reason %q", result, reason)
			}
			if testCase.wantStopped && (!strings.Contains(result.StopReason, reason) || !strings.Contains(result.FeedbackMessage, reason)) {
				t.Fatalf("stop result = %#v, want reason %q", result, reason)
			}
			denied := result.PermissionRequestDecision != nil && result.PermissionRequestDecision.Kind == HookPermissionRequestDeny
			if denied != testCase.wantDeny {
				t.Fatalf("permission decision = %#v, want deny=%t", result.PermissionRequestDecision, testCase.wantDeny)
			}
			if testCase.wantDeny && (result.PermissionRequestDecision.Message == nil || *result.PermissionRequestDecision.Message != reason) {
				t.Fatalf("permission deny message = %#v", result.PermissionRequestDecision.Message)
			}
		})
	}
}

// The default continue policy keeps the previous behavior: a failure is
// reported but never blocks.
func TestHookFailurePolicyDefaultsToContinueLikeRust(t *testing.T) {
	failing := "exit 3"
	metadata := hookRunnerMetadata("continue", HookEventPreToolUse, "", 0)
	metadata.Command = &failing
	metadata.ExecutionMode = HookExecutionSync
	result, err := NewHookRunner().Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventPreToolUse,
		Hooks:     []HookMetadata{metadata},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunFailed || result.Blocked {
		t.Fatalf("run = %#v blocked=%t, want a failed run that continues", result.Runs, result.Blocked)
	}
}

// Discovery keeps the policy on the handler and hashes it, while block is
// rejected for events and handlers that cannot block.
func TestHookDiscoveryKeepsAndValidatesFailurePolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `{
	  "hooks": {
	    "PreToolUse": [
	      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo check", "onFailure": "block"}]},
	      {"matcher": "Bash", "hooks": [{"type": "command", "command": "echo check", "onFailure": "continue"}]},
	      {"matcher": "Read", "hooks": [{"type": "command", "command": "echo async", "async": true, "onFailure": "block"}]}
	    ],
	    "SessionEnd": [
	      {"hooks": [{"type": "command", "command": "echo end", "onFailure": "block"}]}
	    ],
	    "PostToolUse": [
	      {"hooks": [{"type": "command", "command": "echo continue", "onFailure": "continue"}]}
	    ]
	  }
	}`
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	response := NewHookDiscoveryService(home).Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 {
		t.Fatalf("Discover() = %+v", response)
	}
	entry := response.Data[0]
	var blocked *HookMetadata
	var continued *HookMetadata
	for i := range entry.Hooks {
		hook := &entry.Hooks[i]
		if hook.EventName != HookEventPreToolUse || hook.Command == nil || *hook.Command != "echo check" {
			continue
		}
		switch hook.OnFailure {
		case HookFailureBlock:
			blocked = hook
		case HookFailureContinue:
			continued = hook
		}
	}
	if blocked == nil || continued == nil {
		t.Fatalf("PreToolUse hooks = %#v, want block and continue policies", entry.Hooks)
	}
	if blocked.CurrentHash == continued.CurrentHash {
		t.Fatal("failure policy did not participate in the hook hash")
	}
	wantWarning := "onFailure:block requires a synchronous hook that supports blocking in " + filepath.Join(home, "hooks.json")
	var asyncRejected, sessionEndRejected bool
	for _, warning := range entry.Warnings {
		if warning == wantWarning {
			asyncRejected = true
		}
		if strings.Contains(warning, "onFailure:block requires a synchronous hook that supports blocking") && strings.Contains(warning, filepath.Join(home, "hooks.json")) {
			sessionEndRejected = true
		}
	}
	if !asyncRejected || !sessionEndRejected {
		t.Fatalf("warnings = %#v, want both unsupported block handlers rejected", entry.Warnings)
	}
}

// The app-server configRequirements/read surface exposes the policy as a
// nullable onFailure on command and MCP tool handlers.
func TestConfiguredHookHandlerSerializesFailurePolicyLikeRust(t *testing.T) {
	block := "block"
	command := config.ConfiguredHookHandler{Type: "command", Command: "echo check", OnFailure: &block}
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("Marshal(command) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(command) error = %v", err)
	}
	if decoded["onFailure"] != "block" {
		t.Fatalf("command onFailure = %#v", decoded["onFailure"])
	}
	mcp := config.ConfiguredHookHandler{Type: "mcp_tool", Server: "scanner", Tool: "check"}
	encoded, err = json.Marshal(mcp)
	if err != nil {
		t.Fatalf("Marshal(mcp) error = %v", err)
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(mcp) error = %v", err)
	}
	value, present := decoded["onFailure"]
	if !present || value != nil {
		t.Fatalf("mcp onFailure = %#v present=%t, want null", value, present)
	}
}

// TOML hook declarations parse the policy with either spelling.
func TestHookDiscoveryParsesTOMLFailurePolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `[hooks]

[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "echo check"
onFailure = "block"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	response := NewHookDiscoveryService(home).Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(response.Data) != 1 || len(response.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %#v", response)
	}
	hook := response.Data[0].Hooks[0]
	if hook.OnFailure != HookFailureBlock {
		t.Fatalf("TOML hook onFailure = %q, want block", hook.OnFailure)
	}
}
