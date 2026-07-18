package app

import (
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
