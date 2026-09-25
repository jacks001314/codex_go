package appserver

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"codex_go/compact"
	"codex_go/metrics"
	"codex_go/session"
	"codex_go/turn"
)

// previousModelCompaction decides whether the pre-turn compaction runs against
// the previous model. Rust's `maybe_run_previous_model_inline_compact` compacts
// with the model the thread used before when the model's compaction
// compatibility hash changed or when switching to a smaller context-window
// model, so the persisted history stays compatible with the new model.
type previousModelCompaction struct {
	Run           bool
	Reason        compact.Reason
	PreviousModel string
	// PreviousProgram is the access program recorded for the previous turn. Rust
	// restores it after `with_model`, so compaction keeps that turn's
	// model/program pair instead of inheriting the current selection (#48224).
	PreviousProgram string
}

// runtimeModelContextWindow mirrors Rust's `TurnContext::model_context_window`:
// an explicit `model_context_window` config override wins over the catalog
// value, and zero means the window is unknown (Rust's `None`).
func (r *RuntimeRouter) runtimeModelContextWindow(modelID string, params *turn.TurnStartParams) int64 {
	if r == nil {
		return 0
	}
	if cfg, err := r.effectiveConfigForTurn(params); err == nil && cfg != nil {
		if window := intFromAny(cfg.Values["model_context_window"]); window > 0 {
			return int64(window)
		}
	}
	info := r.modelInfoForRuntime(strings.TrimSpace(modelID))
	if info == nil {
		return 0
	}
	if info.ContextWindow > 0 {
		return info.ContextWindow
	}
	return info.MaxContextWindow
}

// previousModelCompactionForTurn mirrors Rust's decision inputs: the thread's
// recorded previous-turn settings (model + compaction hash), the current model
// and its hash, and - for the model-downshift arm - the current model's context
// window and token limit.
func (r *RuntimeRouter) previousModelCompactionForTurn(
	threadID string,
	record *session.Record,
	runConfig *appTurnRunConfig,
	params *turn.TurnStartParams,
) previousModelCompaction {
	if r == nil || record == nil || runConfig == nil {
		return previousModelCompaction{}
	}
	previousModel, previousHash, previousProgram, ok := r.runtimePreviousTurnSettings(threadID, record)
	if !ok {
		return previousModelCompaction{}
	}
	currentModel := strings.TrimSpace(runConfig.Model)
	if currentModel == "" {
		return previousModelCompaction{}
	}
	if compactionHashChanged(previousHash, r.modelCompHash(currentModel)) {
		return previousModelCompaction{Run: true, Reason: compact.ReasonCompHashChanged, PreviousModel: previousModel, PreviousProgram: previousProgram}
	}
	if previousModel == currentModel {
		return previousModelCompaction{}
	}
	previousWindow := r.runtimeModelContextWindow(previousModel, params)
	currentWindow := r.runtimeModelContextWindow(currentModel, params)
	if previousWindow <= 0 || currentWindow <= 0 || previousWindow <= currentWindow {
		return previousModelCompaction{}
	}
	// Rust `previous_model_limit_reached`: the downsized model's own auto-compact
	// limit (or hard window) must already be reached.
	if !r.compactTokenStatusForTurn(threadID, currentModel, params).ShouldCompact {
		return previousModelCompaction{}
	}
	return previousModelCompaction{Run: true, Reason: compact.ReasonModelSwitch, PreviousModel: previousModel, PreviousProgram: previousProgram}
}

// compactionHashChanged mirrors Rust's `comp_hash_changed`: a missing hash on
// either side does not provide enough information to trigger compaction.
func compactionHashChanged(previous string, current string) bool {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	return previous != "" && current != "" && previous != current
}

// runPreviousModelInlineCompact runs the pre-turn compaction with the previous
// model and, when that attempt fails for a reason that is not an abort,
// interruption, or budget stop, retries it with the selected model (#46324).
// It reports whether an attempt ran.
func (r *RuntimeRouter) runPreviousModelInlineCompact(
	ctx context.Context,
	threadID string,
	turnID string,
	connectionID string,
	params *turn.TurnStartParams,
	runConfig *appTurnRunConfig,
	record *session.Record,
) (bool, error) {
	decision := r.previousModelCompactionForTurn(threadID, record, runConfig, params)
	if !decision.Run {
		return false, nil
	}
	status := r.compactTokenStatusForTurn(threadID, decision.PreviousModel, params)
	// Rust's V2 auto-compaction path retries with the selected model instead of
	// summarizing locally, so a remote-capable thread must surface the failed
	// attempt.
	remoteOnly := r.providerSupportsRemoteCompact(record.Metadata.ModelProvider)
	_, err := r.compactThread(ctx, &runtimeCompactRequest{
		ThreadID:     threadID,
		TurnID:       turnID,
		ConnectionID: connectionID,
		Trigger:      compact.TriggerAuto,
		Reason:       decision.Reason,
		Phase:        compact.PhasePreTurn,
		Model:        decision.PreviousModel,
		// The previous model pairs with the previous turn's program; an absent
		// program must not inherit the current selection (Rust #48224).
		CyberAccessProgram:        appStringPointer(decision.PreviousProgram),
		RemoteOnly:                remoteOnly,
		ActiveContextTokensBefore: int64(status.ActiveContextTokens),
	})
	if err == nil {
		return true, nil
	}
	if !shouldRetryCompactionWithCurrentModel(err) {
		return true, err
	}
	fallbackStatus := r.compactTokenStatusForTurn(threadID, runConfig.Model, params)
	_, fallbackErr := r.compactThread(ctx, &runtimeCompactRequest{
		ThreadID:     threadID,
		TurnID:       turnID,
		ConnectionID: connectionID,
		Trigger:      compact.TriggerAuto,
		Reason:       decision.Reason,
		Phase:        compact.PhasePreTurn,
		Model:        runConfig.Model,
		// The retry compacts with the selected model, so it uses the current
		// turn's program.
		CyberAccessProgram:        appCyberAccessProgramForTurnPointer(params, runConfig.ProviderID),
		RemoteOnly:                remoteOnly,
		ActiveContextTokensBefore: int64(fallbackStatus.ActiveContextTokens),
	})
	r.recordCompactionModelFallback(decision.PreviousModel, runConfig.Model, decision.Reason, fallbackErr)
	if fallbackErr != nil {
		// Rust reports the failed previous-model attempt when the fallback also
		// fails.
		return true, err
	}
	return true, nil
}

// shouldRetryCompactionWithCurrentModel mirrors Rust's #46324 predicate: a
// failed compaction retries with the selected model for every error except the
// terminal turn states. Go surfaces an aborted or interrupted turn as a
// canceled context (Rust's `TurnAborted` / `Interrupted`) and the shared
// rollout budget stop as `ErrSessionBudgetExceeded`.
func shouldRetryCompactionWithCurrentModel(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrSessionBudgetExceeded) {
		return false
	}
	return true
}

// recordCompactionModelFallback mirrors Rust's `record_model_fallback`: the
// counter is labelled with the compaction reason, the implementation, and
// whether the retry succeeded, and the attempt is logged.
func (r *RuntimeRouter) recordCompactionModelFallback(previousModel string, currentModel string, reason compact.Reason, fallbackErr error) {
	outcome := "succeeded"
	if fallbackErr != nil {
		outcome = "failed"
	}
	metrics.Counter("codex.compaction.model_fallback", 1, map[string]string{
		"reason":         modelFallbackReasonTag(reason),
		"implementation": compactionAnalyticsImplementation(nil),
		"outcome":        outcome,
	})
	slog.Warn("previous-model compaction failed; retried with current model",
		"previous_model", previousModel,
		"current_model", currentModel,
		"reason", modelFallbackReasonTag(reason),
		"outcome", outcome,
		"error", fallbackErr,
	)
}

// modelFallbackReasonTag mirrors Rust's CompactionReason tags.
func modelFallbackReasonTag(reason compact.Reason) string {
	switch reason {
	case compact.ReasonModelSwitch:
		return "model_downshift"
	case compact.ReasonCompHashChanged:
		return "comp_hash_changed"
	case compact.ReasonContextWindowExceeded, compact.ReasonTokenLimit:
		return "context_limit"
	default:
		return "user_requested"
	}
}
