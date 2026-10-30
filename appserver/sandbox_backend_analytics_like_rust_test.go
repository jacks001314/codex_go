package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/sandbox"
	"codex_go/telemetry"
	"codex_go/tool"
)

func commandExecutionItemWithSandboxBackend(id string, status CommandExecutionStatus, backend *string) *ThreadItem {
	return &ThreadItem{
		ID:     id,
		Type:   "commandExecution",
		CallID: id,
		Data: map[string]any{
			"status":         string(status),
			"command":        "echo sandbox",
			"cwd":            "/tmp",
			"source":         string(CommandExecutionSourceAgent),
			"commandActions": []any{},
		},
		sandboxBackend: backend,
	}
}

// Rust #47568's analytics reducer maps the internal Option<SandboxType> onto a
// nullable sandbox_backend string. Missing must stay distinct from explicit
// unsandboxed execution ("none").
func TestCommandExecutionAnalyticsReportsSandboxBackendLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-sandbox-backend"
		turnID       = "turn-sandbox-backend"
	)
	cases := []struct {
		name    string
		backend *string
		want    *string
	}{
		{name: "unobserved launch", backend: nil, want: nil},
		{name: "explicit none", backend: sandboxBackendTagValue("none"), want: stringPointerAppserver("none")},
		{name: "seatbelt", backend: sandboxBackendTagValue("seatbelt"), want: stringPointerAppserver("seatbelt")},
		{name: "seccomp", backend: sandboxBackendTagValue("seccomp"), want: stringPointerAppserver("seccomp")},
		{name: "windows sandbox", backend: sandboxBackendTagValue("windows_sandbox"), want: stringPointerAppserver("windows_sandbox")},
		{name: "windows mxc", backend: sandboxBackendTagValue("windows_mxc"), want: stringPointerAppserver("windows_mxc")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := commandExecutionItemWithSandboxBackend("cmd-"+strings.ReplaceAll(tc.name, " ", "-"), CommandExecutionCompleted, tc.backend)
			router.emitCommandExecutionAnalyticsEvent(context.Background(), connectionID, threadID, turnID, item, &appTurnRunConfig{})
			params := waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams
			if !equalOptionalString(params.SandboxBackend, tc.want) {
				t.Fatalf("sandbox_backend = %#v, want %#v", params.SandboxBackend, tc.want)
			}
		})
	}
}

func TestFailedCommandExecutionKeepsSandboxBackendLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-sandbox-backend-failed"
		turnID       = "turn-sandbox-backend-failed"
	)
	item := commandExecutionItemWithSandboxBackend("cmd-sandbox-failed", CommandExecutionFailed, sandboxBackendTagValue("seccomp"))
	item.Data["exitCode"] = int64(1)
	router.emitCommandExecutionAnalyticsEvent(context.Background(), connectionID, threadID, turnID, item, &appTurnRunConfig{})
	params := waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams
	if params.SandboxBackend == nil || *params.SandboxBackend != "seccomp" {
		t.Fatalf("sandbox_backend = %#v, want seccomp", params.SandboxBackend)
	}
	if params.TerminalStatus != telemetry.ToolItemTerminalStatusFailed || params.FailureKind == nil || *params.FailureKind != telemetry.ToolItemFailureKindToolError {
		t.Fatalf("terminal outcome = %q/%#v, want failed/tool_error", params.TerminalStatus, params.FailureKind)
	}
}

// The backend is internal: it must not appear on the client item, the thread
// history shape or generated schemas (Rust serde(skip)/schemars(skip)/ts(skip)).
func TestCommandExecutionSandboxBackendStaysOffWireLikeRust(t *testing.T) {
	item := commandExecutionItemWithSandboxBackend("cmd-sandbox-wire", CommandExecutionCompleted, sandboxBackendTagValue("seatbelt"))
	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"sandboxBackend", "sandbox_backend", "sandbox_type", "sandboxType"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("serialized command item leaked %q: %s", forbidden, payload)
		}
	}
}

func TestUnifiedExecEventCarriesSandboxBackendLikeRust(t *testing.T) {
	seccomp := sandbox.SandboxTypeLinuxSeccomp
	startedAt := time.Now().UTC()
	begin := unifiedExecThreadItem(tool.UnifiedExecEvent{
		Kind:        tool.UnifiedExecEventBegin,
		CallID:      "cmd-unified-sandbox",
		Command:     []string{"echo", "hi"},
		CWD:         "/tmp",
		ProcessID:   7,
		StartedAt:   startedAt,
		SandboxType: nil,
	}, CommandExecutionInProgress)
	if begin.sandboxBackend != nil {
		t.Fatalf("begin sandbox backend = %#v, want nil", begin.sandboxBackend)
	}
	completed := unifiedExecThreadItem(tool.UnifiedExecEvent{
		Kind:        tool.UnifiedExecEventEnd,
		CallID:      "cmd-unified-sandbox",
		Command:     []string{"echo", "hi"},
		CWD:         "/tmp",
		ProcessID:   7,
		Output:      "hi\n",
		StartedAt:   startedAt,
		Duration:    time.Second,
		SandboxType: &seccomp,
	}, CommandExecutionCompleted)
	if completed.sandboxBackend == nil || *completed.sandboxBackend != "seccomp" {
		t.Fatalf("completed sandbox backend = %#v, want seccomp", completed.sandboxBackend)
	}
}

func TestUserShellCompletionReportsNoneSandboxBackendLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-user-shell-sandbox"
		turnID       = "turn-user-shell-sandbox"
	)
	exitCode := int64(0)
	durationMS := int64(12)
	run := &threadShellCommandRun{ThreadID: threadID, TurnID: turnID, Command: "echo hi"}
	item := threadShellCommandItem(run, "user-shell-sandbox", "process-1", CommandExecutionCompleted, "hi\n", &exitCode, &durationMS, 1, sandboxBackendTagValue("none"))
	if item.sandboxBackend == nil || *item.sandboxBackend != "none" {
		t.Fatalf("user shell sandbox backend = %#v, want explicit none", item.sandboxBackend)
	}
	router.emitCommandExecutionAnalyticsEvent(context.Background(), connectionID, threadID, turnID, &item, &appTurnRunConfig{Model: "gpt-5"})
	params := waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams
	if params.SandboxBackend == nil || *params.SandboxBackend != "none" {
		t.Fatalf("sandbox_backend = %#v, want none", params.SandboxBackend)
	}
	if params.ModelSlug != nil || params.ReasoningEffort != nil {
		t.Fatalf("user shell model attribution = %#v/%#v, want null/null", params.ModelSlug, params.ReasoningEffort)
	}
	if params.StartedAtMS != 1 || params.CompletedAtMS != 13 || params.DurationMS == nil || *params.DurationMS != 12 {
		t.Fatalf("user shell timing = %d/%d/%#v, want 1/13/12", params.StartedAtMS, params.CompletedAtMS, params.DurationMS)
	}
}

func TestRuntimeRouterUserShellAnalyticsReportsNoneSandboxBackendLikeRust(t *testing.T) {
	analytics := newRecordingTurnEventSink()
	notifications := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadExtras:          NewThreadExtraService(),
		DefaultCWD:            t.TempDir(),
		ThreadStatus:          NewThreadStatusManager(),
		Analytics:             analytics,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportInProcess,
	})
	router.SetNotificationSink(notifications)
	const connectionID = "conn-user-shell-analytics"
	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-tui", Version: "1.2.3"},
	})
	initialize.ConnectionID = connectionID
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	const threadID = "thread-user-shell-analytics"
	router.requireThreadStatus().UpsertThread(threadID, false)
	request := requestWithParams(t, IntID(2), MethodThreadShellCommand, ShellCommandParams{
		ThreadID: threadID,
		Command:  "echo user-shell-sandbox",
	})
	request.ConnectionID = connectionID
	if response := router.Handle(request); response.Error != nil {
		t.Fatalf("thread/shellCommand error: %+v", response.Error)
	}
	started := waitForCommandExecutionStartedBySource(t, notifications, CommandExecutionSourceUserShell)
	params := waitForCommandExecutionAnalyticsEvent(t, analytics, started.TurnID).EventParams
	if params.SandboxBackend == nil || *params.SandboxBackend != "none" {
		t.Fatalf("sandbox_backend = %#v, want none", params.SandboxBackend)
	}
	if params.ModelSlug != nil || params.ReasoningEffort != nil {
		t.Fatalf("user shell model attribution = %#v/%#v, want null/null", params.ModelSlug, params.ReasoningEffort)
	}
}
