package agent

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// MaxRetainedShutdownFailures bounds the failures kept for one agent tree, so a
// long-lived tree cannot retain an unbounded number of them (Rust #51515:
// MAX_RETAINED_SHUTDOWN_FAILURES).
const MaxRetainedShutdownFailures = 64

// Agent tree shutdown failure reasons, matching Rust's
// AgentTreeShutdownFailureReason variants.
const (
	AgentTreeShutdownReasonOperationFailed = "operation_failed"
	AgentTreeShutdownReasonGuardAbandoned  = "guard_abandoned"
)

// AgentTreeShutdownFailure is one failure observed while shutting down an agent
// tree. It never carries a raw error payload: the reason is a stable,
// non-sensitive classification (Rust #51515 AgentTreeShutdownFailure).
type AgentTreeShutdownFailure struct {
	// Operation names the tracked operation that failed.
	Operation string `json:"operation"`
	// ThreadID is the affected thread when the failure is thread-scoped.
	ThreadID string `json:"thread_id,omitempty"`
	// Reason is AgentTreeShutdownReasonOperationFailed or
	// AgentTreeShutdownReasonGuardAbandoned.
	Reason string `json:"reason"`
	// Phase names the step that failed (operation_failed only).
	Phase string `json:"phase,omitempty"`
	// ErrorKind is a stable, payload-free error category (operation_failed only).
	ErrorKind string `json:"error_kind,omitempty"`
}

// AgentTreeShutdownOperationFailed classifies an operation failure without
// retaining arbitrary error messages.
func AgentTreeShutdownOperationFailed(operation, phase, threadID, errorKind string) AgentTreeShutdownFailure {
	return AgentTreeShutdownFailure{
		Operation: operation,
		ThreadID:  strings.TrimSpace(threadID),
		Reason:    AgentTreeShutdownReasonOperationFailed,
		Phase:     phase,
		ErrorKind: errorKind,
	}
}

// AgentTreeShutdownGuardAbandoned reports a tracked operation that was dropped
// before it could report completion.
func AgentTreeShutdownGuardAbandoned(operation, threadID string) AgentTreeShutdownFailure {
	return AgentTreeShutdownFailure{
		Operation: operation,
		ThreadID:  strings.TrimSpace(threadID),
		Reason:    AgentTreeShutdownReasonGuardAbandoned,
	}
}

// AgentTreeShutdownReport is the bounded set of failures observed for one local
// agent-tree instance (Rust #51515 AgentTreeShutdownReport).
type AgentTreeShutdownReport struct {
	// TreeID is a correlation ID for the runtime instance, not a thread ID.
	TreeID string `json:"tree_id"`
	// Failures are retained in recording order.
	Failures []AgentTreeShutdownFailure `json:"failures"`
	// OmittedFailures counts failures dropped after the retention limit.
	OmittedFailures int `json:"omitted_failures"`
}

// Empty reports whether the tree shut down without any recorded failure.
func (r AgentTreeShutdownReport) Empty() bool {
	return len(r.Failures) == 0 && r.OmittedFailures == 0
}

// String formats the report with the same fields and stable order as Rust's
// AgentTreeShutdownReport Display impl, without exposing raw error payloads.
func (r AgentTreeShutdownReport) String() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "agent tree shutdown did not complete cleanly: tree_id=%s, failures=[", r.TreeID)
	for index, failure := range r.Failures {
		if index > 0 {
			builder.WriteString(", ")
		}
		fmt.Fprintf(&builder, "{operation=%s", failure.Operation)
		if failure.Reason == AgentTreeShutdownReasonOperationFailed {
			fmt.Fprintf(&builder, ", phase=%s", failure.Phase)
		}
		if strings.TrimSpace(failure.ThreadID) != "" {
			fmt.Fprintf(&builder, ", thread_id=%s", failure.ThreadID)
		}
		if failure.Reason == AgentTreeShutdownReasonOperationFailed {
			fmt.Fprintf(&builder, ", reason=operation_failed, error_kind=%s", failure.ErrorKind)
		} else {
			builder.WriteString(", reason=guard_abandoned")
		}
		builder.WriteString("}")
	}
	fmt.Fprintf(&builder, "]; omitted_failures=%d", r.OmittedFailures)
	return builder.String()
}

// Error lets the report satisfy the error interface, so an unclean shutdown can
// be reported without losing the structured detail (Rust impl Error for
// AgentTreeShutdownReport).
func (r AgentTreeShutdownReport) Error() string { return r.String() }

// AgentTreeShutdownState records the shutdown failures observed for one agent
// tree. Every recorded failure is also logged, matching Rust's record_failure.
type AgentTreeShutdownState struct {
	mu     sync.Mutex
	report AgentTreeShutdownReport
}

// NewAgentTreeShutdownState creates a recorder for one tree correlation ID.
func NewAgentTreeShutdownState(treeID string) *AgentTreeShutdownState {
	return &AgentTreeShutdownState{report: AgentTreeShutdownReport{TreeID: strings.TrimSpace(treeID)}}
}

// RecordFailure retains a failure (up to MaxRetainedShutdownFailures) and logs
// it. Later failures increment OmittedFailures instead.
func (s *AgentTreeShutdownState) RecordFailure(failure AgentTreeShutdownFailure) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if len(s.report.Failures) < MaxRetainedShutdownFailures {
		s.report.Failures = append(s.report.Failures, failure)
	} else {
		s.report.OmittedFailures++
	}
	treeID := s.report.TreeID
	s.mu.Unlock()
	slog.Warn("agent tree shutdown failure recorded",
		"tree_id", treeID,
		"operation", failure.Operation,
		"phase", failure.Phase,
		"thread_id", failure.ThreadID,
		"reason", failure.Reason,
		"error_kind", failure.ErrorKind,
	)
}

// Report returns a snapshot of the recorded failures. Repeated reads observe the
// same completed report.
func (s *AgentTreeShutdownState) Report() AgentTreeShutdownReport {
	if s == nil {
		return AgentTreeShutdownReport{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	report := s.report
	report.Failures = append([]AgentTreeShutdownFailure(nil), s.report.Failures...)
	return report
}
