package telemetry

import (
	"context"
)

// Rust parity: codex-rs/otel/src/subagent.rs (#62567, `65454c7b99`).
//
// Three counters describe a subagent's lifecycle: a successful spawn, each child
// turn attempt (one start and at most one outcome), and an accepted stop. Thread,
// turn, and call ids only ride the trace event so distinct workers correlate
// without multiplying the metric series; labels stay bounded.

// SubagentTurnStatus values mirror Rust's `SubagentTurnStatus` snake_case names.
const (
	SubagentTurnStatusStarted     = "started"
	SubagentTurnStatusCompleted   = "completed"
	SubagentTurnStatusFailed      = "failed"
	SubagentTurnStatusInterrupted = "interrupted"
	SubagentTurnStatusSuspended   = "suspended"
)

// Subagent turn kinds mirror Rust's turn_metrics.rs labels: a new child's first
// turn, its later turns, and the first turn after an explicit resume.
const (
	SubagentTurnKindInitial       = "initial"
	SubagentTurnKindFollowUp      = "follow_up"
	SubagentTurnKindResumedThread = "resumed_thread"
)

// Subagent stop actions mirror Rust's `SubagentStopAction`.
const (
	SubagentStopActionClose     = "close"
	SubagentStopActionInterrupt = "interrupt"
)

// SubagentStopPreviousStatusUnknown is Rust's fallback when the target had no
// observable status before the stop was accepted.
const SubagentStopPreviousStatusUnknown = "unknown"

// SubagentStopCallerRoot/SubagentStopCallerSubagent are Rust's `caller` labels.
const (
	SubagentStopCallerRoot     = "root"
	SubagentStopCallerSubagent = "subagent"
)

// MultiAgentSpawned describes one successful subagent spawn.
type MultiAgentSpawned struct {
	RootThreadID  string
	ParentTurnID  string
	ChildThreadID string
	ProductSKU    string
}

// MultiAgentTurnLifecycle describes one child turn attempt boundary.
type MultiAgentTurnLifecycle struct {
	ChildThreadID string
	TurnID        string
	TurnKind      string
	Status        string
	ErrorKind     string
	AbortReason   string
	ProductSKU    string
}

// MultiAgentStopAccepted describes one accepted close/interrupt command.
type MultiAgentStopAccepted struct {
	RootThreadID   string
	ChildThreadID  string
	CallerThreadID string
	CallID         string
	Caller         string
	Action         string
	PreviousStatus string
	ProductSKU     string
}

// EmitMultiAgentSpawned records a successful spawn: the event links the parent
// turn and child thread, and the counter carries only the bounded product tag.
func EmitMultiAgentSpawned(ctx context.Context, session *SessionTelemetry, sink TurnMetricSink, record MultiAgentSpawned) {
	fields := map[string]string{
		"root_thread_id":  record.RootThreadID,
		"parent_turn_id":  record.ParentTurnID,
		"child_thread_id": record.ChildThreadID,
	}
	addProductSKU(fields, record.ProductSKU)
	if session != nil {
		session.TraceEvent(ctx, MultiAgentSpawnedMetric, fields, nil)
	}
	if sink == nil {
		return
	}
	sink.Counter(MultiAgentSpawnedMetric, 1, productSKUTags(record.ProductSKU))
}

// EmitMultiAgentTurnLifecycle records one start or outcome for a child turn
// attempt. Only the status class and bounded reason labels reach the counter.
func EmitMultiAgentTurnLifecycle(ctx context.Context, session *SessionTelemetry, sink TurnMetricSink, record MultiAgentTurnLifecycle) {
	fields := map[string]string{
		"child_thread_id": record.ChildThreadID,
		"turn_id":         record.TurnID,
		"turn_kind":       record.TurnKind,
		"status":          record.Status,
	}
	if record.AbortReason != "" {
		fields["abort_reason"] = record.AbortReason
	}
	if record.ErrorKind != "" {
		fields["error_kind"] = record.ErrorKind
	}
	addProductSKU(fields, record.ProductSKU)
	if session != nil {
		session.TraceEvent(ctx, MultiAgentTurnLifecycleMetric, fields, nil)
	}
	if sink == nil {
		return
	}
	tags := map[string]string{
		"turn_kind": record.TurnKind,
		"status":    record.Status,
	}
	if record.ErrorKind != "" {
		tags["error_kind"] = record.ErrorKind
	}
	if record.AbortReason != "" {
		tags["abort_reason"] = record.AbortReason
	}
	addProductSKUTags(tags, record.ProductSKU)
	sink.Counter(MultiAgentTurnLifecycleMetric, 1, tags)
}

// EmitMultiAgentStopAccepted records an accepted close or interrupt, including
// stops of targets that were already idle or unloaded.
func EmitMultiAgentStopAccepted(ctx context.Context, session *SessionTelemetry, sink TurnMetricSink, record MultiAgentStopAccepted) {
	previous := record.PreviousStatus
	if previous == "" {
		previous = SubagentStopPreviousStatusUnknown
	}
	fields := map[string]string{
		"root_thread_id":   record.RootThreadID,
		"child_thread_id":  record.ChildThreadID,
		"caller_thread_id": record.CallerThreadID,
		"call_id":          record.CallID,
		"caller":           record.Caller,
		"action":           record.Action,
		"previous_status":  previous,
	}
	addProductSKU(fields, record.ProductSKU)
	if session != nil {
		session.TraceEvent(ctx, MultiAgentStopAcceptedMetric, fields, nil)
	}
	if sink == nil {
		return
	}
	tags := map[string]string{
		"caller":          record.Caller,
		"action":          record.Action,
		"previous_status": previous,
	}
	addProductSKUTags(tags, record.ProductSKU)
	sink.Counter(MultiAgentStopAcceptedMetric, 1, tags)
}

func addProductSKU(fields map[string]string, productSKU string) {
	if productSKU != "" {
		fields["product_sku"] = productSKU
	}
}

func addProductSKUTags(tags map[string]string, productSKU string) {
	if productSKU != "" {
		tags["product_sku"] = productSKU
	}
}

func productSKUTags(productSKU string) map[string]string {
	tags := map[string]string{}
	addProductSKUTags(tags, productSKU)
	return tags
}
