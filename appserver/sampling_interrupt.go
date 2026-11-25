package appserver

import (
	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
	"context"
)

// The active turn lock serializes the interrupt fallback with attempt admission.
func (r *RuntimeRouter) requestAgentSamplingInterrupt(threadID, turnID string) bool {
	var accepted bool
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		accepted = active.SamplingInterrupt.Request()
	})
	if !accepted {
		return false
	}
	r.markRuntimeAgentWakesStopped(threadID)
	if r.networkApproval != nil {
		r.networkApproval.cancelPendingForTurn(threadID, turnID)
	}
	if r.codeModeInterruptEnabledForThread(threadID) {
		if runtime := r.codeModeRuntimeForThread(threadID); runtime != nil {
			runtime.InterruptActiveCells()
		}
	}
	return true
}

func (r *RuntimeRouter) runPostCompactHooksUnlessInterrupted(ctx context.Context, hookCtx *compactHookContext, threadID, turnID string) error {
	if active := r.activeRuntimeTurnStateSnapshot(threadID, turnID); active != nil && active.SamplingInterrupt.Requested() {
		return nil
	}
	return r.runPostCompactHooks(ctx, hookCtx)
}

// waitSamplingTerminal keeps direct submissions out of a stopped turn. The
// admission mutex is acquired by the caller only after the terminal wait.
func (r *RuntimeRouter) waitSamplingTerminal(threadID string) {
	if active := r.threads.ActiveTurn(threadID); active != nil && active.SamplingInterrupt.Requested() {
		<-active.SamplingInterrupt.Finished()
	}
}

// Child identity comes from the stored parent relation, independently of the
// optional Responses lineage headers (native children may use appServer source).
func (r *RuntimeRouter) initializeSamplingInterrupt(threadID, turnID string, params *turn.TurnStartParams) {
	record := r.runtimeRecordForThread(threadID)
	if record == nil || record.ParentThreadID == "" {
		return
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return
	}
	v2, err := cfg.MultiAgentV2Config(0)
	if err != nil || !turn.SamplingInterruptEnabled(v2.InterruptAtItemBoundary, r.turnAnalyticsMultiAgentVersion(threadID, cfg), string(record.ParentThreadID)) {
		return
	}
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		if active.SamplingInterrupt == nil {
			active.SamplingInterrupt = turn.NewSamplingInterrupt(turnID)
		}
	})
}

// An interrupted mid-turn compaction has already installed a new context usage
// estimate. Charge the old response without replacing that estimate with the
// pre-compaction high-water mark.
func (r *RuntimeRouter) persistInterruptedCompactionUsage(threadID string, aggregate model.AgentUsage) (*compact.TokenStatus, *TokenUsage, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil, nil, err
	}
	extra := cloneAnyMap(record.Metadata.Extra)
	total := tokenUsageBreakdownFromMetadata(extra["total_token_usage"])
	if total == nil {
		total = &TokenUsageBreakdown{}
	}
	addAgentUsageToBreakdown(total, aggregate)
	extra["total_token_usage"] = tokenUsageBreakdownMetadataMap(total)
	if old, ok := extra["token_usage_info"].(map[string]any); ok {
		info := cloneAnyMap(old)
		info["total_token_usage"] = extra["total_token_usage"]
		extra["token_usage_info"] = info
	}
	status := compactTokenStatusFromMetadata(extra)
	usage := &TokenUsage{Total: total, Last: tokenUsageBreakdownFromMetadata(extra["last_token_usage"])}
	if runtimeRecordEphemeral(record) {
		record.Metadata.Extra = extra
		r.saveEphemeralThreadRecord(record)
	} else {
		_, err = r.runtimeUpdateThreadMetadata(session.ThreadID(threadID), &session.MetadataPatch{Extra: extra}, true)
	}
	return &status, usage, err
}
