package app

import (
	"strings"
	"testing"
	"time"
)

func TestHandleExitModeDecisionMatchesRust(t *testing.T) {
	if ShutdownFirstExitTimeout != 2*time.Second {
		t.Fatalf("ShutdownFirstExitTimeout = %s", ShutdownFirstExitTimeout)
	}

	shutdown := HandleExitModeDecision(ExitModeShutdownFirst, "active", "widget")
	if shutdown.PendingShutdownExitThreadID != "active" || !shutdown.ShouldShutdownCurrentThread || shutdown.ShutdownTimeout != 2*time.Second {
		t.Fatalf("shutdown decision = %#v", shutdown)
	}
	if shutdown.Control.Kind != AppRunControlExit || shutdown.Control.Reason != ExitReasonUserRequested {
		t.Fatalf("shutdown control = %#v", shutdown.Control)
	}

	widgetFallback := HandleExitModeDecision(ExitModeShutdownFirst, "", "widget")
	if widgetFallback.PendingShutdownExitThreadID != "widget" || !widgetFallback.ShouldShutdownCurrentThread {
		t.Fatalf("widget fallback = %#v", widgetFallback)
	}

	immediate := HandleExitModeDecision(ExitModeImmediate, "active", "widget")
	if immediate.PendingShutdownExitThreadID != "" || immediate.ShouldShutdownCurrentThread || immediate.ShutdownTimeout != 0 {
		t.Fatalf("immediate decision = %#v", immediate)
	}
	if immediate.Control.Kind != AppRunControlExit || immediate.Control.Reason != ExitReasonUserRequested {
		t.Fatalf("immediate control = %#v", immediate.Control)
	}
}

func TestCurrentThreadMutationPreflightMatchesRust(t *testing.T) {
	missingArchive := CurrentThreadMutationPreflight(CurrentThreadMutationArchive, "", "", nil)
	if missingArchive.Allowed || missingArchive.Message != "A thread must start before it can be archived." {
		t.Fatalf("missing archive = %#v", missingArchive)
	}

	missingDelete := CurrentThreadMutationPreflight(CurrentThreadMutationDelete, "", "", nil)
	if missingDelete.Allowed || missingDelete.Message != "A thread must start before it can be deleted." {
		t.Fatalf("missing delete = %#v", missingDelete)
	}

	sideArchive := CurrentThreadMutationPreflight(CurrentThreadMutationArchive, "side-1", "", map[string]bool{"side-1": true})
	if sideArchive.Allowed || sideArchive.ThreadID != "side-1" || sideArchive.Message != "'/archive' is unavailable in side conversations. Press Ctrl+C to return to the main thread first." {
		t.Fatalf("side archive = %#v", sideArchive)
	}

	sideDelete := CurrentThreadMutationPreflight(CurrentThreadMutationDelete, "", "side-1", map[string]bool{"side-1": true})
	if sideDelete.Allowed || sideDelete.ThreadID != "side-1" || sideDelete.Message != "'/delete' is unavailable in side conversations. Press Ctrl+C to return to the main thread first." {
		t.Fatalf("side delete = %#v", sideDelete)
	}

	allowed := CurrentThreadMutationPreflight(CurrentThreadMutationArchive, "", "thread-1", map[string]bool{"side-1": true})
	if !allowed.Allowed || allowed.ThreadID != "thread-1" || allowed.Message != "" {
		t.Fatalf("allowed = %#v", allowed)
	}
}

func TestExitReasonDistinctionsMatchRust(t *testing.T) {
	// Rust #40629 distinguishes disconnects, interrupted turns, and removed threads.
	if ExitReasonUserRequested == ExitReasonFatal || ExitReasonFatal == ExitReasonTurnInterrupted || ExitReasonTurnInterrupted == ExitReasonThreadRemoved {
		t.Fatal("exit reasons must be distinct")
	}
	if ExitReasonTurnInterrupted != "turn_interrupted" || ExitReasonThreadRemoved != "thread_removed" {
		t.Fatalf("turn/thread exit reason wire values = %q/%q", ExitReasonTurnInterrupted, ExitReasonThreadRemoved)
	}
}

func TestDescribeExitReasonDistinguishesStates(t *testing.T) {
	if got := DescribeExitReason(ExitReasonTurnInterrupted); got != "The active turn was interrupted" {
		t.Fatalf("TurnInterrupted = %q", got)
	}
	if got := DescribeExitReason(ExitReasonThreadRemoved); got != "The thread was removed" {
		t.Fatalf("ThreadRemoved = %q", got)
	}
	if got := DescribeExitReason(ExitReasonFatal); got != "Disconnected from the app server" {
		t.Fatalf("Fatal = %q", got)
	}
}

func TestAppExitInfoFormatsDisconnectGuidance(t *testing.T) {
	info := &AppExitInfo{
		ThreadID:    "thread-1",
		ExitReason:  ExitReasonTurnInterrupted,
		ResumeHint:  "resume: codex resume thread-1",
		Disconnect: &DisconnectInfo{Command: []string{"codex", "--remote", "wss://host:443"}, StopHint: "press esc"},
	}
	lines := info.FormatExitMessages()
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "The active turn was interrupted") || !strings.Contains(joined, "Reconnect: codex --remote wss://host:443") || !strings.Contains(joined, "Stop the running turn: press esc") {
		t.Fatalf("exit summary = %q", joined)
	}
}
