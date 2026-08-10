package appserver

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHookRunnerAsyncHookRunsInBackgroundAndDrains(t *testing.T) {
	sink := NewNotificationBuffer()
	runner := NewHookRunner()
	runner.Notify = sinkNotifyFunc(sink)
	runner.Now = time.Now

	command := hookRunnerOutputCommand(`{"hookSpecificOutput":{"additionalContext":"async-ctx"}}`, "")
	hook := hookRunnerMetadata("hook-async", HookEventUserPromptSubmit, "", 0)
	hook.Command = &command
	hook.ExecutionMode = HookExecutionAsync

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventUserPromptSubmit,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Async hooks do not block the caller: no completed run in the sync result.
	if len(result.Runs) != 0 {
		t.Fatalf("sync runs = %+v, want none for async hook", result.Runs)
	}

	// The started notification is emitted synchronously.
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationHookStarted {
		t.Fatalf("notifications = %+v, want one HookStarted", notifications)
	}

	// Drain results at the turn boundary.
	deadline := time.Now().Add(10 * time.Second)
	var drained []asyncHookResult
	for time.Now().Before(deadline) {
		drained = runner.DrainAsyncResults("thread-1")
		if len(drained) != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(drained) != 1 {
		t.Fatalf("drained = %+v, want one completed async run", drained)
	}
	if drained[0].Run.Status != HookRunCompleted {
		t.Fatalf("drained run status = %s", drained[0].Run.Status)
	}
	if !hookEntriesContain(drained[0].Run.Entries, HookOutputContext, "async-ctx") {
		t.Fatalf("entries = %+v, want context async-ctx", drained[0].Run.Entries)
	}
	// Completed notification is emitted by DrainAsyncResults.
	notifications = sink.List()
	if len(notifications) != 2 || notifications[1].Method != NotificationHookCompleted {
		t.Fatalf("notifications = %+v, want HookCompleted after drain", notifications)
	}
}

func TestHookRunnerSessionEndAsyncStaysSynchronous(t *testing.T) {
	runner := NewHookRunner()
	command := hookRunnerOutputCommand(`{"hookSpecificOutput":{"additionalContext":"end-ctx"}}`, "")
	hook := hookRunnerMetadata("hook-end", HookEventSessionEnd, "", 0)
	hook.Command = &command
	hook.ExecutionMode = HookExecutionAsync

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventSessionEnd,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("runs = %+v, want one synchronous completed run", result.Runs)
	}
}

func TestHookRunnerAsyncConcurrencyLimit(t *testing.T) {
	runner := NewHookRunner()
	command := hookSleepCommand(1)
	hook := hookRunnerMetadata("hook-limit", HookEventUserPromptSubmit, "", 0)
	hook.Command = &command
	hook.ExecutionMode = HookExecutionAsync
	hook.TimeoutSec = 30

	startedAt := time.Now()
	for i := 0; i < 12; i++ {
		_, err := runner.Run(context.Background(), &HookRunRequest{
			ThreadID:  "thread-limit",
			CWD:       t.TempDir(),
			EventName: HookEventUserPromptSubmit,
			InputJSON: "{}",
			Hooks:     []HookMetadata{hook},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	// With a concurrency limit of 8 and 12 × ~1s sleeps, the scheduler must
	// not have run them all in parallel: total elapsed > 1s.
	if elapsed := time.Since(startedAt); elapsed < time.Second {
		t.Fatalf("elapsed = %v, want at least 1s (concurrency limited)", elapsed)
	}
	runner.ShutdownAsync("thread-limit")
}

func TestHookRunnerShutdownAsyncAbortsBackgroundWork(t *testing.T) {
	runner := NewHookRunner()
	command := hookSleepCommand(30)
	hook := hookRunnerMetadata("hook-abort", HookEventUserPromptSubmit, "", 0)
	hook.Command = &command
	hook.ExecutionMode = HookExecutionAsync
	hook.TimeoutSec = 60

	if _, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-abort",
		CWD:       t.TempDir(),
		EventName: HookEventUserPromptSubmit,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	runner.ShutdownAsync("thread-abort")
	// After shutdown the background hook must not deliver a result.
	time.Sleep(200 * time.Millisecond)
	if drained := runner.DrainAsyncResults("thread-abort"); len(drained) != 0 {
		t.Fatalf("drained = %+v, want none after shutdown", drained)
	}
}

func TestHookDiscoveryAsyncSessionEndStaysSync(t *testing.T) {
	if mode := hookDiscoveryExecutionMode(true, HookEventSessionEnd); mode != HookExecutionSync {
		t.Fatalf("SessionEnd async mode = %s, want sync", mode)
	}
	if mode := hookDiscoveryExecutionMode(true, HookEventPreToolUse); mode != HookExecutionAsync {
		t.Fatalf("PreToolUse async mode = %s, want async", mode)
	}
	if mode := hookDiscoveryExecutionMode(false, HookEventStop); mode != HookExecutionSync {
		t.Fatalf("sync mode = %s, want sync", mode)
	}
}

func TestHookProcessTreeTimeoutTerminatesTree(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	runner := NewHookRunner()
	command := hookSleepCommand(30)
	hook := hookRunnerMetadata("hook-timeout", HookEventPreToolUse, "*", 0)
	hook.Command = &command
	hook.TimeoutSec = 1

	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-timeout",
		CWD:       t.TempDir(),
		EventName: HookEventPreToolUse,
		InputJSON: "{}",
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunFailed {
		t.Fatalf("runs = %+v, want one failed (timed out) run", result.Runs)
	}
	if !strings.Contains(result.Runs[0].Entries[0].Text, "timed out") {
		t.Fatalf("entries = %+v, want timeout message", result.Runs[0].Entries)
	}
}

func hookSleepCommand(seconds int) string {
	if runtime.GOOS == "windows" {
		return powershellEncodedCommand("Start-Sleep -Seconds " + strconv.Itoa(seconds))
	}
	return "sleep " + strconv.Itoa(seconds)
}
