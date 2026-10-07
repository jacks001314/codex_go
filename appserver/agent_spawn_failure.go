package appserver

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/tool"
)

// Rust parity: codex-rs/core/src/tools/handlers/multi_agents_common.rs
// `record_collab_spawn_failure` (#51355, `c0c230e673`).
//
// The bounded classifications of one failed spawn are derived here, at the
// layer that returns the spawn error, and recorded through the session
// telemetry plus the turn metrics. The error keeps its message and its
// identity: the annotation only adds the failure origin, so the model-visible
// text (`tool.RespondToModel(err.Error())`) is unchanged.

// errAgentRuntimeUnavailable is the app-server spawn path's manager-unavailable
// failure. Rust reports the same condition as
// `CodexErrorDetails::UnsupportedOperation("thread manager dropped")`, so the
// bounded labels below classify it into the same arms.
var errAgentRuntimeUnavailable = errors.New("agent runtime is unavailable")

// Bounded error kinds, spelled the way Rust's `CodexErrKind` serializes
// (`#[strum(serialize_all = "snake_case")]`). They come from Go's error surface,
// so a duplicate path is classified through `agent.ErrAgentPathExists` into the
// `unsupported_operation` kind Rust uses for the same registry rejection.
const (
	agentSpawnErrorKindAgentLimitReached = "agent_limit_reached"
	agentSpawnErrorKindUnsupported       = "unsupported_operation"
	agentSpawnErrorKindThreadNotFound    = "thread_not_found"
	agentSpawnErrorKindInvalidRequest    = "invalid_request"
	agentSpawnErrorKindIo                = "io"
	agentSpawnErrorKindOther             = "other"
)

// agentSpawnFailureReason mirrors the `reason` arms of Rust's
// `record_collab_spawn_failure`.
func agentSpawnFailureReason(err error) string {
	switch {
	case errors.Is(err, agent.ErrAgentLimitReached):
		return telemetry.AgentSpawnFailureReasonLimitReached
	case errors.Is(err, agent.ErrAgentPathExists):
		return telemetry.AgentSpawnFailureReasonUnsupportedOperation
	case errors.Is(err, errAgentRuntimeUnavailable):
		return telemetry.AgentSpawnFailureReasonUnsupportedOperation
	case errors.Is(err, session.ErrThreadNotFound):
		return telemetry.AgentSpawnFailureReasonThreadNotFound
	case errors.Is(err, ErrJSONRPCInvalidRequest), errors.Is(err, ErrInvalidRequest):
		return telemetry.AgentSpawnFailureReasonInvalidRequest
	default:
		return telemetry.AgentSpawnFailureReasonInternal
	}
}

// agentSpawnFailureErrorKind is the bounded semantic kind of a spawn failure,
// the label Rust takes from `CodexErrKind::from(err)`.
func agentSpawnFailureErrorKind(err error) string {
	switch {
	case err == nil:
		return agentSpawnErrorKindOther
	case errors.Is(err, agent.ErrAgentLimitReached):
		return agentSpawnErrorKindAgentLimitReached
	case errors.Is(err, agent.ErrAgentPathExists), errors.Is(err, errAgentRuntimeUnavailable):
		return agentSpawnErrorKindUnsupported
	case errors.Is(err, session.ErrThreadNotFound):
		return agentSpawnErrorKindThreadNotFound
	case errors.Is(err, ErrJSONRPCInvalidRequest), errors.Is(err, ErrInvalidRequest):
		return agentSpawnErrorKindInvalidRequest
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return agentSpawnErrorKindIo
	}
	return agentSpawnErrorKindOther
}

// recordRuntimeAgentSpawnFailure annotates err with its bounded failure origin,
// records the spawn-failure diagnostics, and returns err for the caller to
// report unchanged.
func recordRuntimeAgentSpawnFailure(c *runtimeAgentController, ctx context.Context, err error, origin tool.AgentErrorContext, forkMode string) error {
	if err == nil {
		return nil
	}
	err = tool.WithAgentErrorContext(err, origin)
	if c == nil || c.router == nil {
		return err
	}
	// Go's agent controller is turn-scoped rather than call-scoped, so the
	// trace event carries the turn id Rust records and leaves `call_id` empty.
	telemetry.EmitMultiAgentSpawnFailure(ctx,
		c.router.sessionTelemetryForThread(c.parentID),
		c.router.services.TurnMetrics,
		telemetry.AgentSpawnFailure{
			Reason:     agentSpawnFailureReason(err),
			Detail:     telemetry.AgentSpawnFailureDetail(err),
			ErrorKind:  agentSpawnFailureErrorKind(err),
			TurnID:     c.parentTurnID,
			ForkMode:   forkMode,
			Version:    string(c.version),
			ProductSKU: c.router.spawnFailureProductSKU(),
		})
	return err
}

// spawnFailureForkMode is the `fork_mode` label of a spawn failure: `all` when
// the spawn would have forked the parent's history and `none` for a fresh
// child, matching Rust's `SpawnAgentForkMode` mapping.
func spawnFailureForkMode(version agent.MultiAgentVersion, forkContext bool, forkTurns string) string {
	if version == agent.VersionV2 {
		if forkTurns != "" && forkTurns != telemetry.AgentSpawnFailureForkModeNone {
			return telemetry.AgentSpawnFailureForkModeAll
		}
		return telemetry.AgentSpawnFailureForkModeNone
	}
	if forkContext {
		return telemetry.AgentSpawnFailureForkModeAll
	}
	return telemetry.AgentSpawnFailureForkModeNone
}

// spawnFailureProductSKU resolves the product sku the spawn-failure labels
// carry, the value Rust's session telemetry holds as `apps_mcp_product_sku`.
func (r *RuntimeRouter) spawnFailureProductSKU() string {
	if r == nil || r.services.Config == nil {
		return ""
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return ""
	}
	return strings.TrimSpace(stringFromMap(read.Config, "apps_mcp_product_sku"))
}
