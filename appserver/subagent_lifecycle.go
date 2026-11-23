package appserver

import (
	"context"
	"strings"
	"sync"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/telemetry"
)

// Rust parity: codex-rs/core/src/agent/turn_metrics.rs (#62567).
//
// A child turn attempt reports one start and at most one outcome, even when the
// same turn id is observed twice (terminal notifications can repeat). The tracker
// is per router because each app-server owns its thread tree.

type subagentTurnAttemptTracker struct {
	mu        sync.Mutex
	kinds     map[string]string
	outcomes  map[string]bool
	resumed   map[string]bool
	reasons   map[string]string
	attempted map[string]bool
}

func subagentAttemptKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "|" + strings.TrimSpace(turnID)
}

// begin records a new attempt and reports whether this is its first start.
func (t *subagentTurnAttemptTracker) begin(threadID, turnID, kind string) bool {
	key := subagentAttemptKey(threadID, turnID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.kinds == nil {
		t.kinds = map[string]string{}
		t.outcomes = map[string]bool{}
		t.reasons = map[string]string{}
	}
	if _, exists := t.kinds[key]; exists {
		return false
	}
	t.kinds[key] = kind
	t.outcomes[key] = false
	return true
}

func (t *subagentTurnAttemptTracker) kindOf(threadID, turnID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	kind, ok := t.kinds[subagentAttemptKey(threadID, turnID)]
	return kind, ok
}

// claimOutcome marks the attempt's outcome as recorded and reports whether this
// call won the race.
func (t *subagentTurnAttemptTracker) claimOutcome(threadID, turnID string) bool {
	key := subagentAttemptKey(threadID, turnID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.kinds[key]; !ok {
		return false
	}
	if t.outcomes[key] {
		return false
	}
	t.outcomes[key] = true
	return true
}

// claimFirstAttempt reports whether this thread has not reported an attempt yet.
// Rust keeps the same `first_turn_pending` flag per spawned thread, so the first
// observed attempt is `initial` and later ones are follow-ups.
func (t *subagentTurnAttemptTracker) claimFirstAttempt(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.attempted == nil {
		t.attempted = map[string]bool{}
	}
	if t.attempted[threadID] {
		return false
	}
	t.attempted[threadID] = true
	return true
}

// noteAbortReason remembers the bounded reason an accepted interrupt asked for.
// Rust reads it off TurnAborted; Go's terminal status carries no reason, so the
// interrupt path hands over the value it already validated.
func (t *subagentTurnAttemptTracker) noteAbortReason(threadID, turnID, reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	key := subagentAttemptKey(threadID, turnID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.kinds[key]; !ok {
		return
	}
	if t.reasons == nil {
		t.reasons = map[string]string{}
	}
	t.reasons[key] = reason
}

func (t *subagentTurnAttemptTracker) abortReason(threadID, turnID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reasons[subagentAttemptKey(threadID, turnID)]
}

func (t *subagentTurnAttemptTracker) markResumed(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resumed == nil {
		t.resumed = map[string]bool{}
	}
	t.resumed[threadID] = true
}

func (t *subagentTurnAttemptTracker) takeResumed(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.resumed[threadID] {
		return false
	}
	delete(t.resumed, threadID)
	return true
}

// recordSubagentSpawn mirrors Rust's `record_multi_agent_spawned`: the event
// links the parent turn to the child thread and the counter counts the spawn.
func (r *RuntimeRouter) recordSubagentSpawn(ctx context.Context, rootThreadID, parentTurnID, childThreadID string) {
	if r == nil || strings.TrimSpace(childThreadID) == "" {
		return
	}
	telemetry.EmitMultiAgentSpawned(ctx, r.sessionTelemetryForThread(rootThreadID), r.services.TurnMetrics, telemetry.MultiAgentSpawned{
		RootThreadID:  rootThreadID,
		ParentTurnID:  parentTurnID,
		ChildThreadID: childThreadID,
		ProductSKU:    r.spawnFailureProductSKU(),
	})
}

// noteSubagentResume marks the child's next attempt as a resumed one.
func (r *RuntimeRouter) noteSubagentResume(threadID string) {
	if r == nil {
		return
	}
	r.subagentTurns.markResumed(threadID)
}

// runtimeRecordIsSpawnedSubagent reports whether a record belongs to a spawned
// subagent: every child carries a parent thread id, and delegated sessions also
// match runtimeRecordIsSubagent.
func runtimeRecordIsSpawnedSubagent(record *session.Record) bool {
	if record == nil {
		return false
	}
	return strings.TrimSpace(string(record.ParentThreadID)) != "" || runtimeRecordIsSubagent(record)
}

// recordSubagentTurnStarted records the start of a child turn attempt. The kind
// is `resumed` after an explicit resume_agent, `initial` for the child's first
// turn, and `followup` otherwise.
func (r *RuntimeRouter) recordSubagentTurnStarted(ctx context.Context, threadID, turnID string) {
	if r == nil || strings.TrimSpace(turnID) == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || !runtimeRecordIsSpawnedSubagent(record) {
		return
	}
	if _, recorded := r.subagentTurns.kindOf(threadID, turnID); recorded {
		return
	}
	kind := telemetry.SubagentTurnKindFollowUp
	switch {
	case r.subagentTurns.takeResumed(threadID):
		kind = telemetry.SubagentTurnKindResumedThread
	case r.subagentTurns.claimFirstAttempt(threadID):
		kind = telemetry.SubagentTurnKindInitial
	}
	if !r.subagentTurns.begin(threadID, turnID, kind) {
		return
	}
	telemetry.EmitMultiAgentTurnLifecycle(ctx, r.sessionTelemetryForThread(threadID), r.services.TurnMetrics, telemetry.MultiAgentTurnLifecycle{
		ChildThreadID: threadID,
		TurnID:        turnID,
		TurnKind:      kind,
		Status:        telemetry.SubagentTurnStatusStarted,
		ProductSKU:    r.spawnFailureProductSKU(),
	})
}

// recordSubagentTurnOutcome records the single outcome of a started attempt.
// Rust labels a failure with the turn's CodexErrorInfo class and an interrupt
// with the TurnAborted reason; Go's notified turn already carries the error, and
// the interrupt path stores the reason before the terminal status arrives.
func (r *RuntimeRouter) recordSubagentTurnOutcome(ctx context.Context, threadID, turnID string, status TurnStatus, turnError *TurnError) {
	if r == nil {
		return
	}
	kind, ok := r.subagentTurns.kindOf(threadID, turnID)
	if !ok || !r.subagentTurns.claimOutcome(threadID, turnID) {
		return
	}
	outcome := string(status)
	abortReason := ""
	errorKind := ""
	switch status {
	case TurnStatusCompleted:
		outcome = telemetry.SubagentTurnStatusCompleted
	case TurnStatusFailed:
		outcome = telemetry.SubagentTurnStatusFailed
		errorKind = subagentErrorKind(turnError)
	case TurnStatusInterrupted:
		outcome = telemetry.SubagentTurnStatusInterrupted
		abortReason = firstNonEmpty(r.subagentTurns.abortReason(threadID, turnID), turnAbortReasonInterrupted)
	case "":
		outcome = telemetry.SubagentTurnStatusCompleted
	}
	telemetry.EmitMultiAgentTurnLifecycle(ctx, r.sessionTelemetryForThread(threadID), r.services.TurnMetrics, telemetry.MultiAgentTurnLifecycle{
		ChildThreadID: threadID,
		TurnID:        turnID,
		TurnKind:      kind,
		Status:        outcome,
		ErrorKind:     errorKind,
		AbortReason:   abortReason,
		ProductSKU:    r.spawnFailureProductSKU(),
	})
}

// recordSubagentStopAccepted mirrors Rust's `record_multi_agent_stop_accepted`:
// an accepted close or interrupt counts even when the target was idle.
func (r *RuntimeRouter) recordSubagentStopAccepted(ctx context.Context, rootThreadID, callerThreadID, childThreadID, action string, previous agent.AgentMessageStatus) {
	if r == nil || strings.TrimSpace(childThreadID) == "" {
		return
	}
	caller := telemetry.SubagentStopCallerSubagent
	if strings.TrimSpace(callerThreadID) == strings.TrimSpace(rootThreadID) {
		caller = telemetry.SubagentStopCallerRoot
	}
	previousStatus := strings.TrimSpace(string(previous.Kind))
	if previousStatus == "" {
		previousStatus = telemetry.SubagentStopPreviousStatusUnknown
	}
	telemetry.EmitMultiAgentStopAccepted(ctx, r.sessionTelemetryForThread(callerThreadID), r.services.TurnMetrics, telemetry.MultiAgentStopAccepted{
		RootThreadID:   rootThreadID,
		ChildThreadID:  childThreadID,
		CallerThreadID: callerThreadID,
		Caller:         caller,
		Action:         action,
		PreviousStatus: previousStatus,
		ProductSKU:     r.spawnFailureProductSKU(),
	})
}

// subagentErrorKind renders Rust's CodexErrorInfo label, the snake_case variant
// name. Go carries the wire value as untyped JSON: a plain string is the variant
// and a single-key object names its structured variant.
func subagentErrorKind(turnError *TurnError) string {
	if turnError == nil || turnError.CodexErrorInfo == nil {
		return ""
	}
	switch info := turnError.CodexErrorInfo.(type) {
	case string:
		return snakeCaseVariant(info)
	case map[string]any:
		if len(info) != 1 {
			return ""
		}
		for key := range info {
			return snakeCaseVariant(key)
		}
	}
	return ""
}

func snakeCaseVariant(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
