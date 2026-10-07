package telemetry

import (
	"context"

	"codex_go/tool"
)

// Rust parity: codex-rs/otel/src/events/session_telemetry.rs
// `SessionTelemetry::record_multi_agent_spawn_failure` and
// codex-rs/protocol/src/agent_error.rs (#51355, `c0c230e673`).
//
// A failed multi-agent spawn reports two bounded classifications alongside the
// reason and the fork mode: the failure origin an annotated error carries
// (`detail`, `unknown` when there is none) and the error's own semantic kind
// (`error_kind`). Messages and correlation ids are never labels; they only ride
// the trace event, which is why distinct private messages aggregate into one
// bounded series.

// AgentSpawnFailureDetailUnknown is Rust's fallback label for a spawn error that
// carries no annotation (`err.agent_context().map_or("unknown", ...)`).
const AgentSpawnFailureDetailUnknown = "unknown"

// Spawn-failure reasons: the arms Rust's `record_collab_spawn_failure` maps a
// spawn error onto (core/src/tools/handlers/multi_agents_common.rs).
const (
	AgentSpawnFailureReasonLimitReached         = "limit_reached"
	AgentSpawnFailureReasonInvalidRequest       = "invalid_request"
	AgentSpawnFailureReasonThreadNotFound       = "thread_not_found"
	AgentSpawnFailureReasonUnsupportedOperation = "unsupported_operation"
	AgentSpawnFailureReasonInternal             = "internal"
)

// Spawn-failure fork modes, matching the `fork_mode` label Rust derives from
// `SpawnAgentForkMode` (`none` for a fresh child, `all` for a full-history
// fork).
const (
	AgentSpawnFailureForkModeNone = "none"
	AgentSpawnFailureForkModeAll  = "all"
)

// AgentSpawnFailureDetail reports the bounded failure origin of a spawn error,
// or Rust's `unknown` when the error carries no annotation.
func AgentSpawnFailureDetail(err error) string {
	if context, ok := tool.AgentErrorContextOf(err); ok {
		return string(context)
	}
	return AgentSpawnFailureDetailUnknown
}

// AgentSpawnFailure is one failed multi-agent spawn: the bounded
// classifications plus the correlation ids Rust's trace event carries.
type AgentSpawnFailure struct {
	Reason     string
	Detail     string
	ErrorKind  string
	CallID     string
	TurnID     string
	ForkMode   string
	Version    string
	ProductSKU string
}

// EmitMultiAgentSpawnFailure records one failed multi-agent spawn: the
// trace-safe event carries the bounded classifications with the call and turn
// ids, and the counter carries the same bounded labels. Either sink may be
// absent (Rust's `record_multi_agent_spawn_failure` skips the counter when the
// session has no metrics and its tracing layer drops an event outside a span).
func EmitMultiAgentSpawnFailure(ctx context.Context, session *SessionTelemetry, sink TurnMetricSink, failure AgentSpawnFailure) {
	fields := map[string]string{
		"reason":              failure.Reason,
		"detail":              failure.Detail,
		"error_kind":          failure.ErrorKind,
		"call_id":             failure.CallID,
		"turn_id":             failure.TurnID,
		"fork_mode":           failure.ForkMode,
		"multi_agent_version": failure.Version,
	}
	if failure.ProductSKU != "" {
		fields["product_sku"] = failure.ProductSKU
	}
	if session != nil {
		session.TraceEvent(ctx, MultiAgentSpawnFailureMetric, fields, nil)
	}
	if sink == nil {
		return
	}
	tags := map[string]string{
		"reason":              failure.Reason,
		"detail":              failure.Detail,
		"error_kind":          failure.ErrorKind,
		"fork_mode":           failure.ForkMode,
		"multi_agent_version": failure.Version,
	}
	if failure.ProductSKU != "" {
		tags["product_sku"] = failure.ProductSKU
	}
	sink.Counter(MultiAgentSpawnFailureMetric, 1, tags)
}
