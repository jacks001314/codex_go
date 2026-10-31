package execserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/sandbox"
)

func TestProcessTelemetryRecordsLaunchLifecycleLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	server := NewServer()
	server.SetExecutorRegistrationIdentity("environment-1", "registration-1")
	ctx := withTraceContext(context.Background(), TraceContext{TraceID: "trace-1", SpanID: "span-1"})
	threadID := "thread-1"
	toolCallID := "call-1"
	params := &ExecParams{
		ProcessID: "telemetry-lifecycle",
		Metadata:  &ExecMetadata{ThreadID: &threadID, ToolCallID: &toolCallID},
		Argv:      []string{"go", "version"},
		CWD:       pathToURI(t.TempDir()),
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}
	if _, err := server.startProcess(ctx, params); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	waitForProcessExitLikeRust(t, server, params.ProcessID)

	start := logs.waitForLog(t, "codex.exec_server.process_start", func(attrs map[string]any) bool {
		return attrs["tool.call_id"] == toolCallID
	})
	assertProcessTelemetryAttrsLikeRust(t, start, "codex.exec_server.process_start", "None")
	if start["launch.trace_id"] != "trace-1" || start["launch.span_id"] != "span-1" {
		t.Fatalf("launch trace = %#v", start)
	}
	if start["conversation.id"] != threadID {
		t.Fatalf("conversation id = %#v, want %q", start["conversation.id"], threadID)
	}
	if start["executor.environment_id"] != "environment-1" || start["executor.registration_id"] != "registration-1" {
		t.Fatalf("executor identity = %#v", start)
	}

	exit := logs.waitForLog(t, "codex.exec_server.process_exit", func(attrs map[string]any) bool {
		return attrs["tool.call_id"] == toolCallID
	})
	assertProcessTelemetryAttrsLikeRust(t, exit, "codex.exec_server.process_exit", "None")
	if exit["process.exit_code"] != int64(0) || exit["process.termination_requested"] != false {
		t.Fatalf("exit fields = %#v", exit)
	}
}

func TestProcessTelemetryRecordsSpawnFailureLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	server := NewServer()
	threadID := "thread-spawn-failure"
	toolCallID := "call-spawn-failure"
	_, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "telemetry-spawn-failure",
		Metadata:  &ExecMetadata{ThreadID: &threadID, ToolCallID: &toolCallID},
		Argv:      []string{"codex-go-command-that-does-not-exist"},
		CWD:       pathToURI(t.TempDir()),
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	})
	if err == nil {
		t.Fatal("startProcess() error = nil, want spawn failure")
	}
	failed := logs.waitForLog(t, "codex.exec_server.process_spawn_failed", func(attrs map[string]any) bool {
		return attrs["tool.call_id"] == toolCallID
	})
	assertProcessTelemetryAttrsLikeRust(t, failed, "codex.exec_server.process_spawn_failed", "None")
}

func TestProcessTelemetryRecordsSandboxDenialAndTerminationLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	threadID := "thread-denied"
	toolCallID := "call-denied"
	state := &processState{
		id:          "telemetry-denied",
		sandboxType: sandbox.SandboxTypeLinuxSeccomp,
		telemetry: newProcessTelemetry(context.Background(), &ExecParams{
			Metadata: &ExecMetadata{ThreadID: &threadID, ToolCallID: &toolCallID},
		}, &executorRegistrationIdentity{environmentID: "environment-1", registrationID: "registration-1"}),
		chunks:      []outputChunk{{Chunk: []byte("Operation not permitted")}},
		terminateFn: func() error { return nil },
	}
	state.cond = sync.NewCond(&state.mu)
	if response := state.terminate(); !response.Running {
		t.Fatalf("terminate response = %#v, want running", response)
	}
	exitCode := 1
	state.finishWithCode(nil, &exitCode)

	denied := logs.waitForLog(t, "codex.exec_server.sandbox_denied", func(attrs map[string]any) bool {
		return attrs["tool.call_id"] == toolCallID
	})
	assertProcessTelemetryAttrsLikeRust(t, denied, "codex.exec_server.sandbox_denied", "LinuxSeccomp")
	if denied["reason"] != "inferred_denial" {
		t.Fatalf("sandbox denial reason = %#v", denied["reason"])
	}
	exit := logs.waitForLog(t, "codex.exec_server.process_exit", func(attrs map[string]any) bool {
		return attrs["tool.call_id"] == toolCallID
	})
	if exit["process.exit_code"] != int64(exitCode) || exit["process.termination_requested"] != true {
		t.Fatalf("exit fields = %#v", exit)
	}
}

func TestProcessTelemetryBoundsMetadataLikeRust(t *testing.T) {
	longThreadID := "thread"
	for len(longThreadID) <= maxProcessTelemetryIDBytes {
		longThreadID += "x"
	}
	badToolCallID := "call with spaces"
	telemetry := newProcessTelemetry(context.Background(), &ExecParams{
		Metadata: &ExecMetadata{ThreadID: &longThreadID, ToolCallID: &badToolCallID},
	}, &executorRegistrationIdentity{environmentID: "bad\x00environment", registrationID: "registration-1"})
	if telemetry.threadID != "" || telemetry.toolCallID != "" || telemetry.executorEnvironmentID != "" || telemetry.executorRegistrationID != "" {
		t.Fatalf("telemetry = %#v, want invalid values omitted", telemetry)
	}
}

func assertProcessTelemetryAttrsLikeRust(t *testing.T, attrs map[string]any, eventName string, sandboxName string) {
	t.Helper()
	if attrs == nil {
		t.Fatalf("%s event is missing", eventName)
	}
	if attrs["target"] != "codex_otel.log_only" || attrs["event.name"] != eventName || attrs["sandbox.type"] != sandboxName {
		t.Fatalf("%s attrs = %#v", eventName, attrs)
	}
}

func waitForProcessExitLikeRust(t *testing.T, server *Server, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := server.lookup(processID)
		if state == nil {
			t.Fatalf("process %q disappeared before exit", processID)
		}
		state.mu.Lock()
		exited := state.exited
		state.mu.Unlock()
		if exited {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process %q did not exit", processID)
}
