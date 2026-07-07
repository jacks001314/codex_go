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
