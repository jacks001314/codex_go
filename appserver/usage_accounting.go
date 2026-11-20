package appserver

import (
	"fmt"
	"strings"

	"codex_go/model"
	"codex_go/runtimeutil"
	"codex_go/turn"
)

// UsageAccounting is the host hook for completed-response spending (Rust
// AgentRuntimeServices::Host usage_accounting, #61958). Installing it replaces
// the native rollout budget for provider usage and post-hook charges.
type UsageAccounting = runtimeutil.UsageAccounting

// rolloutBudgetSettlement charges one completed response before its tool calls
// are dispatched (Rust Session::record_rollout_budget_usage, #61577/#61958).
// A host decision of UsageAccountingExhaustedForTurn surfaces as
// ErrSessionBudgetExceeded for this turn only; the shared rollout budget is
// left untouched, so later turns and peer threads stay eligible.
func (r *RuntimeRouter) rolloutBudgetSettlement(threadID string, turnID string) turn.SamplingSettlement {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	return func(ctx *turn.SamplingFollowUpContext) error {
		if ctx == nil || ctx.Response == nil {
			return nil
		}
		usage := runtimeutil.TokenUsage{
			InputTokens:             ctx.Response.Usage.InputTokens,
			CachedInputTokens:       ctx.Response.Usage.CachedInputTokens,
			OutputTokens:            ctx.Response.Usage.OutputTokens,
			CodexRolloutBudgetUnits: ctx.Response.Usage.CodexRolloutBudgetUnits,
		}
		if accounting := r.services.UsageAccounting; accounting != nil {
			if !rolloutBudgetUsagePresent(usage) {
				return r.turnBudgetExhaustedError(threadID, turnID)
			}
			decision, err := accounting.ReportUsage(runtimeutil.AgentUsageReport{
				ThreadID: threadID,
				TurnID:   turnID,
				Usage:    &usage,
			})
			if err != nil {
				return fmt.Errorf("%w: Host usage accounting failed: %v", model.ErrUsageAccountingFailed, err)
			}
			if err := r.applyUsageAccountingDecision(threadID, turnID, decision); err != nil {
				return err
			}
			return r.turnBudgetExhaustedError(threadID, turnID)
		}
		budget := r.rolloutBudgetForThread(threadID)
		if budget == nil {
			return r.turnBudgetExhaustedError(threadID, turnID)
		}
		r.rolloutBudgetCharged.Store(true)
		exhausted, err := budget.RecordUsage(usage)
		if err != nil {
			// Rust maps a native accounting error to UsageAccountingFailed, so
			// the turn fails as `usage_accounting_failed` instead of ignoring
			// the unpostable bill.
			return fmt.Errorf("%w: %v", model.ErrUsageAccountingFailed, err)
		}
		if exhausted {
			r.rolloutBudgetExhausted.Store(true)
		}
		return r.turnBudgetExhaustedError(threadID, turnID)
	}
}

// reportHookChargeToHost hands one synchronous post-hook price to the host's
// accounting (Rust #61580/#61958). It reports false when the native rollout
// budget owns hook charges instead.
func (r *RuntimeRouter) reportHookChargeToHost(threadID string, turnID string, charge runtimeutil.HookCharge) (bool, error) {
	if r == nil {
		return false, nil
	}
	accounting := r.services.UsageAccounting
	if accounting == nil {
		return false, nil
	}
	decision, err := accounting.ReportUsage(runtimeutil.AgentUsageReport{
		ThreadID:              strings.TrimSpace(threadID),
		TurnID:                strings.TrimSpace(turnID),
		AdditionalBudgetUnits: charge.Units,
	})
	if err != nil {
		return true, fmt.Errorf("%w: Host usage accounting failed: %v", model.ErrUsageAccountingFailed, err)
	}
	return true, r.applyUsageAccountingDecision(threadID, turnID, decision)
}

// stopNativeBudgetOnOverflow declines new work after an unrepresentable hook
// total when a native rollout budget enforces its limit. A host-owned bill
// leaves the local budget untouched (Rust #62559).
func (r *RuntimeRouter) stopNativeBudgetOnOverflow(threadID string) {
	if r == nil || r.services.UsageAccounting != nil {
		return
	}
	budget := r.rolloutBudgetForThread(threadID)
	if budget == nil {
		return
	}
	if config, ok := budget.Config(); ok && !config.Advisory {
		budget.StopOnOverflow()
	}
}

// applyUsageAccountingDecision records what an Exhausted decision means for
// the thread's rollout tree. ExhaustedForTurn is answered when this turn's
// settlement runs, so the completed response's output and usage survive.
func (r *RuntimeRouter) applyUsageAccountingDecision(threadID string, turnID string, decision runtimeutil.UsageAccountingDecision) error {
	switch decision {
	case runtimeutil.UsageAccountingExhausted:
		r.markHostBudgetExhausted(threadID)
	case runtimeutil.UsageAccountingExhaustedForTurn:
		r.markTurnBudgetExhausted(threadID, turnID)
	}
	return nil
}

func (r *RuntimeRouter) turnBudgetExhaustedError(threadID string, turnID string) error {
	if r.consumeTurnBudgetExhausted(threadID, turnID) {
		return ErrSessionBudgetExceeded
	}
	return nil
}

func (r *RuntimeRouter) markTurnBudgetExhausted(threadID string, turnID string) {
	if r == nil {
		return
	}
	key := rolloutBudgetTurnKey(threadID, turnID)
	if key == "" {
		return
	}
	r.rolloutBudgetsMu.Lock()
	defer r.rolloutBudgetsMu.Unlock()
	if r.turnBudgetExhausted == nil {
		r.turnBudgetExhausted = map[string]struct{}{}
	}
	r.turnBudgetExhausted[key] = struct{}{}
}

// consumeTurnBudgetExhausted reports and clears a turn-scoped exhaustion so a
// later turn of the same thread starts unpoisoned.
func (r *RuntimeRouter) consumeTurnBudgetExhausted(threadID string, turnID string) bool {
	if r == nil {
		return false
	}
	key := rolloutBudgetTurnKey(threadID, turnID)
	if key == "" {
		return false
	}
	r.rolloutBudgetsMu.Lock()
	defer r.rolloutBudgetsMu.Unlock()
	if _, ok := r.turnBudgetExhausted[key]; !ok {
		return false
	}
	delete(r.turnBudgetExhausted, key)
	return true
}

func (r *RuntimeRouter) markHostBudgetExhausted(threadID string) {
	if r == nil {
		return
	}
	root := r.rolloutBudgetRootThread(threadID)
	if root == "" {
		return
	}
	r.rolloutBudgetsMu.Lock()
	defer r.rolloutBudgetsMu.Unlock()
	if r.hostBudgetExhausted == nil {
		r.hostBudgetExhausted = map[string]struct{}{}
	}
	r.hostBudgetExhausted[root] = struct{}{}
}

// hostBudgetExhaustedFor reports whether the host stopped new work for the
// thread's rollout tree (Rust UsageAccountingDecision::Exhausted).
func (r *RuntimeRouter) hostBudgetExhaustedFor(threadID string) bool {
	if r == nil || r.services.UsageAccounting == nil {
		return false
	}
	root := r.rolloutBudgetRootThread(threadID)
	if root == "" {
		return false
	}
	r.rolloutBudgetsMu.Lock()
	defer r.rolloutBudgetsMu.Unlock()
	_, ok := r.hostBudgetExhausted[root]
	return ok
}

func rolloutBudgetTurnKey(threadID string, turnID string) string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ""
	}
	return threadID + "\x00" + turnID
}

func rolloutBudgetUsagePresent(usage runtimeutil.TokenUsage) bool {
	return usage.InputTokens != 0 || usage.CachedInputTokens != 0 ||
		usage.OutputTokens != 0 || usage.CodexRolloutBudgetUnits != ""
}

// chargeIncompleteUsageLikeRust hands usage reported by a terminal incomplete
// response or a background compaction to the host when one is installed. The
// caller already holds the terminal error, so an accounting failure is
// swallowed (Rust's `let _ = record_rollout_budget_usage`); only the Exhausted
// decision survives because it gates later work. It reports false when the
// native rollout budget owns the charge.
func (r *RuntimeRouter) chargeIncompleteUsageLikeRust(threadID string, turnID string, usage runtimeutil.TokenUsage) bool {
	if r == nil || r.services.UsageAccounting == nil {
		return false
	}
	if !rolloutBudgetUsagePresent(usage) {
		return true
	}
	decision, err := r.services.UsageAccounting.ReportUsage(runtimeutil.AgentUsageReport{
		ThreadID: strings.TrimSpace(threadID),
		TurnID:   strings.TrimSpace(turnID),
		Usage:    &usage,
	})
	if err == nil && decision == runtimeutil.UsageAccountingExhausted {
		r.markHostBudgetExhausted(threadID)
	}
	return true
}
