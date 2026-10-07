package agent

import (
	"strings"
	"testing"
)

// TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust mirrors Rust #51515
// AgentTreeShutdownReport::Display: the report names the operation, phase,
// thread and stable error kind, and never leaks a raw error payload.
func TestAgentTreeShutdownReportFormattingIsPayloadFreeLikeRust(t *testing.T) {
	state := NewAgentTreeShutdownState("tree-1")
	state.RecordFailure(AgentTreeShutdownOperationFailed("session_shutdown", "close_persistence", "thread-2", "thread_store_conflict"))
	state.RecordFailure(AgentTreeShutdownGuardAbandoned("resident_eviction", "thread-3"))
	report := state.Report()
	text := report.String()
	if !strings.HasPrefix(text, "agent tree shutdown did not complete cleanly: tree_id=tree-1, failures=[") {
		t.Fatalf("report prefix = %q", text)
	}
	want := "{operation=session_shutdown, phase=close_persistence, thread_id=thread-2, reason=operation_failed, error_kind=thread_store_conflict}, " +
		"{operation=resident_eviction, thread_id=thread-3, reason=guard_abandoned}"
	if !strings.Contains(text, want) {
		t.Fatalf("report = %q, want it to contain %q", text, want)
	}
	if !strings.HasSuffix(text, "]; omitted_failures=0") {
		t.Fatalf("report suffix = %q", text)
	}
	if report.Error() != text {
		t.Fatalf("Error() = %q, want the Display text", report.Error())
	}
	if report.Empty() {
		t.Fatal("a report with failures must not be empty")
	}
}

// TestAgentTreeShutdownReportRetentionLikeRust mirrors Rust's
// MAX_RETAINED_SHUTDOWN_FAILURES: at most 64 failures are retained and the rest
// are counted as omitted.
func TestAgentTreeShutdownReportRetentionLikeRust(t *testing.T) {
	state := NewAgentTreeShutdownState("tree-retention")
	for i := 0; i < MaxRetainedShutdownFailures+3; i++ {
		state.RecordFailure(AgentTreeShutdownOperationFailed("session_shutdown", "close_persistence", "thread", "operation_error"))
	}
	report := state.Report()
	if len(report.Failures) != MaxRetainedShutdownFailures {
		t.Fatalf("retained failures = %d, want %d", len(report.Failures), MaxRetainedShutdownFailures)
	}
	if report.OmittedFailures != 3 {
		t.Fatalf("omitted failures = %d, want 3", report.OmittedFailures)
	}
}

// TestAgentTreeShutdownReportRepeatedReadsLikeRust mirrors Rust's repeated
// waiters: every read observes the same completed report, and a caller cannot
// mutate the recorder's retained state through the returned snapshot.
func TestAgentTreeShutdownReportRepeatedReadsLikeRust(t *testing.T) {
	state := NewAgentTreeShutdownState("tree-reads")
	state.RecordFailure(AgentTreeShutdownOperationFailed("session_shutdown", "close_persistence", "thread", "operation_error"))
	first := state.Report()
	first.Failures[0].Operation = "mutated"
	second := state.Report()
	if second.Failures[0].Operation != "session_shutdown" {
		t.Fatalf("snapshot aliased recorder state: %#v", second.Failures[0])
	}
	if !strings.Contains(second.String(), "session_shutdown") {
		t.Fatalf("repeated read = %q", second.String())
	}
	empty := NewAgentTreeShutdownState("tree-empty").Report()
	if !empty.Empty() || empty.String() != "agent tree shutdown did not complete cleanly: tree_id=tree-empty, failures=[]; omitted_failures=0" {
		t.Fatalf("empty report = %q, empty=%v", empty.String(), empty.Empty())
	}
}
