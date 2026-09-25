package tool

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"codex_go/execserver"
	"codex_go/sandbox"

	"github.com/google/uuid"
)

// Rust #45505 brackets a session start and its output collection with their own
// spans, and reports how each ended.
func TestUnifiedExecLifecycleSpansMatchRust(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	recorder := &recordingUnifiedExecSpanSink{}
	manager.SetSpanSink(recorder.sink())

	result, err := manager.Exec(context.Background(), &ShellRequest{
		Command:             unifiedExecHelperCommand("immediate"),
		HookCommand:         "immediate helper",
		CWD:                 t.TempDir(),
		YieldTimeMS:         1_000,
		UnifiedExecThreadID: "thread-spans",
		UnifiedExecTurnID:   "turn-spans",
	}, "call-exec")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !result.HasExitCode || result.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}

	openSession := recorder.spanWithName(UnifiedExecOpenSessionSpanName)
	if openSession == nil {
		t.Fatal("no open_session span was recorded")
	}
	if !openSession.wasEnded() || openSession.attribute(UnifiedExecSpanOutcome) != UnifiedExecOutcomeCompleted {
		t.Fatalf("open_session span = %#v", openSession)
	}
	if openSession.attribute(UnifiedExecSpanConversationID) != "thread-spans" ||
		openSession.attribute(UnifiedExecSpanTurnID) != "turn-spans" ||
		openSession.attribute(UnifiedExecSpanCallID) != "call-exec" {
		t.Fatalf("open_session attributes = %#v", openSession.open)
	}

	collect := recorder.spanWithName(UnifiedExecCollectOutputSpanName)
	if collect == nil {
		t.Fatal("no collect_output span was recorded")
	}
	if !collect.wasEnded() || collect.attribute(UnifiedExecSpanOutcome) != UnifiedExecOutcomeCompleted {
		t.Fatalf("collect_output span = %#v", collect)
	}
	// The helper exits before the yield window, so the collection ended because
	// the output closed, with the exit observed and the streams drained.
	if collect.attribute(UnifiedExecSpanStopReason) != UnifiedExecStopReasonOutputClosed ||
		collect.attribute(UnifiedExecSpanExitSignaled) != "true" ||
		collect.attribute(UnifiedExecSpanOutputClosed) != "true" {
		t.Fatalf("collect_output attributes = %#v", collect.open)
	}
}

// write_stdin reports the interaction kind, the original exec call and the
// process, and how the interaction ended.
func TestUnifiedExecWriteStdinSpanMatchesRust(t *testing.T) {
	manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	recorder := &recordingUnifiedExecSpanSink{}
	manager.SetSpanSink(recorder.sink())

	opened, err := manager.Exec(context.Background(), &ShellRequest{
		Command:             unifiedExecHelperCommand("echo"),
		HookCommand:         "interactive helper",
		CWD:                 t.TempDir(),
		TTY:                 true,
		YieldTimeMS:         unifiedExecMinYieldMS,
		TimeoutMS:           15_000,
		UnifiedExecThreadID: "thread-stdin",
		UnifiedExecTurnID:   "turn-stdin",
	}, "call-open")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if opened.ProcessID == nil {
		t.Fatalf("session did not yield: %#v", opened)
	}
	sessionID := *opened.ProcessID

	if _, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{
		SessionID: sessionID, Chars: "hello\n", YieldTimeMS: 1_000, CallID: "call-write",
	}, nil); err != nil {
		t.Fatalf("WriteStdin() error = %v", err)
	}
	write := recorder.spanWithName(UnifiedExecWriteStdinSpanName)
	if write == nil {
		t.Fatal("no write_stdin span was recorded")
	}
	if !write.wasEnded() || write.attribute(UnifiedExecSpanInteraction) != UnifiedExecInteractionWrite {
		t.Fatalf("write_stdin span = %#v", write)
	}
	if write.attribute(UnifiedExecSpanOriginalExecCallID) != "call-open" ||
		write.attribute(UnifiedExecSpanCallID) != "call-write" ||
		write.attribute(UnifiedExecSpanProcessID) != strconv.Itoa(sessionID) {
		t.Fatalf("write_stdin attributes = %#v/%#v", write.open, write.updates)
	}
	if write.attribute(UnifiedExecSpanOutcome) != UnifiedExecOutcomeYielded {
		t.Fatalf("write_stdin outcome = %q", write.attribute(UnifiedExecSpanOutcome))
	}

	// An empty write is a poll.
	if _, err := manager.WriteStdin(context.Background(), &WriteStdinArgs{
		SessionID: sessionID, YieldTimeMS: 1_000, CallID: "call-poll",
	}, nil); err != nil {
		t.Fatalf("poll error = %v", err)
	}
	poll := recorder.spanWithName(UnifiedExecWriteStdinSpanName)
	if poll == nil || poll.attribute(UnifiedExecSpanInteraction) != UnifiedExecInteractionPoll {
		t.Fatalf("poll span = %#v", poll)
	}
}

// Rust #45505 records the exec_command mode and how the call ended. Rust picks
// the mode by handler lifetime (resumable, or the completion-only one-shot
// handler); Go has one handler and picks the path per environment, so the span
// reports the path the call took.
func TestExecCommandSpanMatchesRustModes(t *testing.T) {
	recorder := &recordingUnifiedExecSpanSink{}

	exec := func(runner ShellRunner, manager *UnifiedExecManager) *ShellExecutor {
		options := &ShellExecutorOptions{
			Runner:              runner,
			UnifiedExec:         manager,
			UnifiedExecSpans:    recorder.sink(),
			OneShot:             manager == nil,
			ToolName:            PlainName(DefaultExecCommandToolName),
			Shell:               &Shell{Type: ShellBash, Path: "/bin/sh"},
			UnifiedExecThreadID: "thread-exec",
			UnifiedExecTurnID:   "turn-exec",
			Validation: ShellValidationOptions{
				ApprovalPolicy:   sandbox.ApprovalOnRequest,
				CWD:              t.TempDir(),
				DefaultTimeoutMS: 5000,
			},
		}
		return NewShellExecutor(options)
	}
	invocation := func(callID string) *Invocation {
		return &Invocation{
			CallID:   callID,
			ToolName: PlainName(DefaultExecCommandToolName),
			Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
		}
	}

	// The completion-only path reports the one-shot mode and the outcome Rust's
	// one-shot span records.
	cases := []struct {
		name   string
		runner *fakeShellRunner
		want   string
	}{
		{"exited", &fakeShellRunner{result: &ShellResult{HasExitCode: true}}, UnifiedExecOutcomeExited},
		{"timed_out", &fakeShellRunner{result: &ShellResult{TimedOut: true}}, UnifiedExecOutcomeTimedOut},
		{"failed", &fakeShellRunner{err: errors.New("spawn failed")}, UnifiedExecOutcomeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := exec(tc.runner, nil)
			_, _ = executor.Execute(context.Background(), invocation("call-"+tc.name))
			span := recorder.spanWithName(UnifiedExecExecCommandSpanName)
			if span == nil {
				t.Fatal("no exec_command span was recorded")
			}
			if span.attribute(UnifiedExecSpanMode) != UnifiedExecModeOneshot {
				t.Fatalf("mode = %q", span.attribute(UnifiedExecSpanMode))
			}
			if span.attribute(UnifiedExecSpanCallID) != "call-"+tc.name || span.attribute(UnifiedExecSpanConversationID) != "thread-exec" {
				t.Fatalf("attributes = %#v", span.open)
			}
			if !span.wasEnded() || span.attribute(UnifiedExecSpanOutcome) != tc.want {
				t.Fatalf("outcome = %q", span.attribute(UnifiedExecSpanOutcome))
			}
		})
	}

	// The resumable path reports the yielded process id; a cancelled call reports
	// the cancellation outcome instead.
	manager := NewUnifiedExecManager()
	defer manager.Close()
	manager.SetSpanSink(recorder.sink())
	executor := exec(&fakeShellRunner{}, manager)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.Execute(cancelled, invocation("call-cancelled")); err == nil {
		t.Fatal("a cancelled call must fail")
	}
	span := recorder.spanWithName(UnifiedExecExecCommandSpanName)
	if span == nil {
		t.Fatal("no exec_command span was recorded")
	}
	if span.attribute(UnifiedExecSpanMode) != UnifiedExecModeResumable {
		t.Fatalf("mode = %q", span.attribute(UnifiedExecSpanMode))
	}
	if !span.wasEnded() || span.attribute(UnifiedExecSpanOutcome) != UnifiedExecOutcomeCancelled {
		t.Fatalf("outcome = %q", span.attribute(UnifiedExecSpanOutcome))
	}
}

// Rust's trace_id omits ids that are empty or longer than the byte budget.
func TestUnifiedExecTraceIDMatchesRustBudget(t *testing.T) {
	if _, ok := UnifiedExecTraceID("  "); ok {
		t.Fatal("an empty id must be omitted")
	}
	if _, ok := UnifiedExecTraceID(strings.Repeat("a", MaxUnifiedExecTraceIDBytes+1)); ok {
		t.Fatal("an oversized id must be omitted")
	}
	if id, ok := UnifiedExecTraceID(strings.Repeat("a", MaxUnifiedExecTraceIDBytes)); !ok || len(id) != MaxUnifiedExecTraceIDBytes {
		t.Fatalf("an id at the budget must be kept: %q/%v", id, ok)
	}
	if id, ok := UnifiedExecTraceID(" call-1 "); !ok || id != "call-1" {
		t.Fatalf("id = %q/%v", id, ok)
	}
	if attributes := unifiedExecSpanAttributes("thread-1", "", strings.Repeat("a", MaxUnifiedExecTraceIDBytes+1)); len(attributes) != 1 {
		t.Fatalf("attributes = %#v", attributes)
	}
}

// Rust emits a trace-safe process-start event before an exec-server process
// start, on the session-creation span, because a sandbox retry can reuse the
// public id for a new executor process (#45505).
func TestUnifiedExecRemoteProcessStartEventMatchesRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	var listenOutput lockedUnifiedExecBuffer
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- execserver.NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &listenOutput)
	}()
	remoteURL := waitUnifiedExecServerURL(t, &listenOutput)

	manager := NewUnifiedExecManagerWithOptions(2, unifiedExecMinEmptyPollYieldMS)
	defer manager.Close()
	recorder := &recordingUnifiedExecSpanSink{}
	manager.SetSpanSink(recorder.sink())

	_, err := manager.Exec(context.Background(), &ShellRequest{
		Command:                  unifiedExecHelperCommand("immediate"),
		HookCommand:              "remote immediate helper",
		CWD:                      t.TempDir(),
		TTY:                      false,
		YieldTimeMS:              1_000,
		TimeoutMS:                15_000,
		UnifiedExecRemoteURL:     remoteURL,
		UnifiedExecEnvironmentID: "remote",
		UnifiedExecThreadID:      "thread-remote-event",
		UnifiedExecTurnID:        "turn-remote-event",
	}, "call-remote-event")
	if err != nil {
		t.Fatalf("remote Exec() error = %v", err)
	}

	openSession := recorder.spanWithName(UnifiedExecOpenSessionSpanName)
	if openSession == nil {
		t.Fatal("no open_session span was recorded for the remote start")
	}
	event := openSession.eventWithName(UnifiedExecProcessStartRequestedEvent)
	if event == nil {
		t.Fatalf("no process-start event was recorded: %#v", openSession.events)
	}
	// The executor's id is the public id with a fresh UUID suffix, so a reused
	// public handle never collides with a process the executor still holds
	// (Rust #48168).
	publicID, parseErr := strconv.Atoi(event[UnifiedExecSpanProcessID])
	if parseErr != nil || publicID <= 0 {
		t.Fatalf("process-start event id = %#v", event)
	}
	executorProcessID := event[UnifiedExecSpanExecutorProcessKey]
	suffixed, ok := strings.CutPrefix(executorProcessID, event[UnifiedExecSpanProcessID]+"-")
	if !ok {
		t.Fatalf("executor process id %q is not the public id plus a suffix", executorProcessID)
	}
	if _, err := uuid.Parse(suffixed); err != nil {
		t.Fatalf("process-start event = %#v", event)
	}
}
