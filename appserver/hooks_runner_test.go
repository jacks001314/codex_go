package appserver

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestSelectHookHandlersMatchesRustMatcherRules(t *testing.T) {
	hooks := []HookMetadata{
		hookRunnerMetadata("apply", HookEventPreToolUse, "apply_patch|Write|Edit", 0),
		hookRunnerMetadata("bash", HookEventPreToolUse, "^Bash$", 1),
		hookRunnerMetadata("stop", HookEventStop, "[", 2),
	}

	selected := selectHookHandlers(hooks, HookEventPreToolUse, []string{"apply_patch", "Write", "Edit"})
	if len(selected) != 1 || selected[0].Key != "apply" {
		t.Fatalf("selected = %+v", selected)
	}
	selected = selectHookHandlers(hooks, HookEventPreToolUse, []string{"Bash"})
	if len(selected) != 1 || selected[0].Key != "bash" {
		t.Fatalf("selected bash = %+v", selected)
	}
	selected = selectHookHandlers(hooks, HookEventStop, nil)
	if len(selected) != 1 || selected[0].Key != "stop" {
		t.Fatalf("selected stop = %+v", selected)
	}
}

func TestHookRunnerRunsCommandAndNotifies(t *testing.T) {
	sink := NewNotificationBuffer()
	now := time.UnixMilli(1000)
	runner := NewHookRunner()
	runner.Notify = sinkNotifyFunc(sink)
	runner.Now = func() time.Time {
		current := now
		now = now.Add(125 * time.Millisecond)
		return current
	}
	command := hookRunnerOutputCommand(`{"hookSpecificOutput":{"additionalContext":"ctx"},"systemMessage":"note"}`, "")
	hook := hookRunnerMetadata("hook-1", HookEventSessionStart, "", 0)
	hook.Command = &command

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventSessionStart,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
	if !hookEntriesContain(result.Runs[0].Entries, HookOutputContext, "ctx") || !hookEntriesContain(result.Runs[0].Entries, HookOutputFeedback, "note") {
		t.Fatalf("entries = %+v", result.Runs[0].Entries)
	}
	notifications := sink.List()
	if len(notifications) != 2 || notifications[0].Method != NotificationHookStarted || notifications[1].Method != NotificationHookCompleted {
		t.Fatalf("notifications = %+v", notifications)
	}
}

func TestHookRunnerNonZeroExitFailsRun(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("hook-1", HookEventPreToolUse, "*", 0)
	command := hookRunnerExitCommand(3)
	hook.Command = &command

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:      "thread-1",
		CWD:           t.TempDir(),
		EventName:     HookEventPreToolUse,
		MatcherInputs: []string{"Bash"},
		InputJSON:     "{}",
		Hooks:         []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunFailed {
		t.Fatalf("result = %+v", result)
	}
	if !hookEntriesContain(result.Runs[0].Entries, HookOutputError, "code 3") {
		t.Fatalf("entries = %+v", result.Runs[0].Entries)
	}
}

func TestSelectHookHandlersBypassTrustAllowsUntrustedButNotDisabled(t *testing.T) {
	untrusted := hookRunnerMetadata("untrusted", HookEventPreToolUse, "Bash", 0)
	untrusted.TrustStatus = HookTrustUntrusted
	untrusted.BypassTrust = true
	disabled := hookRunnerMetadata("disabled", HookEventPreToolUse, "Bash", 1)
	disabled.TrustStatus = HookTrustUntrusted
	disabled.BypassTrust = true
	disabled.Enabled = false
	plain := hookRunnerMetadata("plain", HookEventPreToolUse, "Bash", 2)
	plain.TrustStatus = HookTrustUntrusted

	selected := selectHookHandlers([]HookMetadata{untrusted, disabled, plain}, HookEventPreToolUse, []string{"Bash"})
	if len(selected) != 1 || selected[0].Key != "untrusted" {
		t.Fatalf("selected = %+v", selected)
	}
}

func TestHookRunnerAddsPluginEnv(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("plugin", HookEventSessionStart, "", 0)
	hook.Env = map[string]string{"PLUGIN_ROOT": "plugin-root"}
	command := `{"hookSpecificOutput":{"additionalContext":"${PLUGIN_ROOT}"}}`
	if runtime.GOOS == "windows" {
		command = powershellEncodedCommand(`[Console]::Out.Write('{"hookSpecificOutput":{"additionalContext":"' + $env:PLUGIN_ROOT + '"}}')`)
	} else {
		command = `printf '{"hookSpecificOutput":{"additionalContext":"%s"}}' "$PLUGIN_ROOT"`
	}
	hook.Command = &command

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventSessionStart,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("result = %+v", result)
	}
	if !hookEntriesContain(result.Runs[0].Entries, HookOutputContext, "plugin-root") {
		t.Fatalf("entries = %+v", result.Runs[0].Entries)
	}
}

func TestHookRunnerFastExitPreservesOutputWithoutReadingLargeStdinLikeRust(t *testing.T) {
	runner := NewHookRunner()
	hook := hookRunnerMetadata("fast-exit", HookEventSessionStart, "", 0)
	command := hookRunnerOutputCommand("hook-ran", "hook-stderr")
	hook.Command = &command

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-fast-exit",
		CWD:       t.TempDir(),
		EventName: HookEventSessionStart,
		InputJSON: fmt.Sprintf(`{"padding":%q}`, strings.Repeat("x", 1024*1024)),
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 {
		t.Fatalf("runs = %+v, want one", result.Runs)
	}
	run := result.Runs[0]
	if run.Status != HookRunCompleted {
		t.Fatalf("status = %q entries=%+v, want completed", run.Status, run.Entries)
	}
	if !hookEntriesContain(run.Entries, HookOutputContext, "hook-ran") {
		t.Fatalf("stdout was not preserved: entries=%+v", run.Entries)
	}
	if !hookEntriesContain(run.Entries, HookOutputError, "hook-stderr") {
		t.Fatalf("stderr was not preserved: entries=%+v", run.Entries)
	}
}

func hookRunnerMetadata(key string, event HookEventName, matcher string, order int64) HookMetadata {
	command := hookRunnerOutputCommand("", "")
	metadata := HookMetadata{
		Key:          key,
		EventName:    event,
		HandlerType:  HookHandlerCommand,
		Command:      &command,
		TimeoutSec:   5,
		SourcePath:   "/tmp/hooks.json",
		Source:       HookSourceUser,
		DisplayOrder: order,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}
	if matcher != "" {
		metadata.Matcher = &matcher
	}
	return metadata
}

func hookRunnerOutputCommand(stdout string, stderr string) string {
	if runtime.GOOS == "windows" {
		if stdout == "" && stderr == "" {
			return "ver >nul"
		}
		parts := []string{}
		if stdout != "" {
			parts = append(parts, "[Console]::Out.Write("+powerShellSingleQuote(stdout)+")")
		}
		if stderr != "" {
			parts = append(parts, "[Console]::Error.Write("+powerShellSingleQuote(stderr)+")")
		}
		return powershellEncodedCommand(strings.Join(parts, "; "))
	}
	script := ""
	if stdout != "" {
		script += "printf " + shellQuote(stdout)
	}
	if stderr != "" {
		if script != "" {
			script += "; "
		}
		script += "printf " + shellQuote(stderr) + " 1>&2"
	}
	if script == "" {
		script = "true"
	}
	return script
}

// hookRunnerPermissionRequestDenyCommand writes a denial reason to stderr and
// exits 2, Rust's stderr-based PermissionRequest deny.
func hookRunnerPermissionRequestDenyCommand(message string) string {
	if runtime.GOOS == "windows" {
		return powershellEncodedCommand("[Console]::Error.Write(" + powerShellSingleQuote(message) + "); exit 2")
	}
	return "printf " + shellQuote(message) + " 1>&2; exit 2"
}

func hookRunnerExitCommand(code int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("exit /b %d", code)
	}
	return fmt.Sprintf("exit %d", code)
}

func hookEntriesContain(entries []HookOutputEntry, kind HookOutputEntryKind, text string) bool {
	for _, entry := range entries {
		if entry.Kind == kind && strings.Contains(entry.Text, text) {
			return true
		}
	}
	return false
}

func powerShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func powershellEncodedCommand(script string) string {
	return "powershell -NoProfile -EncodedCommand " + powershellEncodedScript(script)
}

func powershellEncodedScript(script string) string {
	encoded := utf16.Encode([]rune(script))
	data := make([]byte, len(encoded)*2)
	for i, value := range encoded {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// Rust resolve_permission_request_decision: any deny wins, otherwise the last
// allow wins, and a handler that cannot apply control effects contributes no
// verdict.
func TestHookRunnerFoldsPermissionRequestDecisionLikeRust(t *testing.T) {
	run := func(t *testing.T, hooks ...HookMetadata) *HookRunResult {
		t.Helper()
		runner := NewHookRunner()
		result, err := runner.Run(context.Background(), &HookRunRequest{
			ThreadID:  "thread-1",
			CWD:       t.TempDir(),
			EventName: HookEventPermissionRequest,
			InputJSON: "{}",
			Hooks:     hooks,
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		return result
	}
	allow := func(t *testing.T, key string, order int64) HookMetadata {
		t.Helper()
		command := hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`, "")
		hook := hookRunnerMetadata(key, HookEventPermissionRequest, "", order)
		hook.Command = &command
		return hook
	}
	deny := func(t *testing.T, key string, order int64, message string) HookMetadata {
		t.Helper()
		command := hookRunnerOutputCommand(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"`+message+`"}}}`, "")
		hook := hookRunnerMetadata(key, HookEventPermissionRequest, "", order)
		hook.Command = &command
		return hook
	}

	if result := run(t, allow(t, "a", 0), allow(t, "b", 1)); result.PermissionRequestDecision == nil ||
		result.PermissionRequestDecision.Kind != HookPermissionRequestAllow {
		t.Fatalf("allows = %#v", result.PermissionRequestDecision)
	}
	denied := run(t, allow(t, "a", 0), deny(t, "b", 1, "repo deny"), allow(t, "c", 2))
	if denied.PermissionRequestDecision == nil || denied.PermissionRequestDecision.Kind != HookPermissionRequestDeny ||
		denied.PermissionRequestDecision.Message == nil || *denied.PermissionRequestDecision.Message != "repo deny" {
		t.Fatalf("deny fold = %#v", denied.PermissionRequestDecision)
	}
	if !denied.Blocked || denied.BlockReason != "repo deny" {
		t.Fatalf("deny did not block: %#v", denied)
	}
	if result := run(t); result.PermissionRequestDecision != nil {
		t.Fatalf("no handlers produced a verdict: %#v", result.PermissionRequestDecision)
	}

	// An untrusted handler's verdict is never applied (Rust
	// can_apply_control_effects).
	untrusted := allow(t, "u", 0)
	untrusted.TrustStatus = HookTrustUntrusted
	untrusted.BypassTrust = false
	if result := run(t, untrusted); result.PermissionRequestDecision != nil {
		t.Fatalf("untrusted handler produced a verdict: %#v", result.PermissionRequestDecision)
	}

	// Exit code 2 with a denial reason is a deny verdict.
	exitTwo := hookRunnerMetadata("e2", HookEventPermissionRequest, "", 0)
	command := hookRunnerPermissionRequestDenyCommand("policy says no")
	exitTwo.Command = &command
	fromExit := run(t, exitTwo)
	if fromExit.PermissionRequestDecision == nil || fromExit.PermissionRequestDecision.Kind != HookPermissionRequestDeny ||
		fromExit.PermissionRequestDecision.Message == nil || *fromExit.PermissionRequestDecision.Message != "policy says no" {
		t.Fatalf("exit-code-2 deny = %#v", fromExit.PermissionRequestDecision)
	}
}
