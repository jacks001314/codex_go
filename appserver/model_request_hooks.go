package appserver

import (
	"context"
	"fmt"
	"strings"

	"codex_go/model"
	"codex_go/runtimeutil"
	"codex_go/turn"
)

// modelRequestHookCallback wires the turn-level Pre/PostModelRequest events
// into the thread's discovered hooks (Rust #55144).
func (r *RuntimeRouter) modelRequestHookCallback(threadID string, sessionID string, cwd string) turn.ModelRequestHook {
	hook := r.modelRequestAttemptHook(threadID, sessionID, cwd)
	if hook == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	cwd = strings.TrimSpace(cwd)
	return func(ctx context.Context, event turn.ModelRequestHookEvent) error {
		var usage *model.AgentUsage
		if event.Usage != nil {
			value := *event.Usage
			usage = &value
		}
		return hook(ctx, model.ModelRequestHookEvent{
			Phase:         event.Phase,
			Status:        event.Status,
			Kind:          model.SamplingKind(event.Kind),
			RequestID:     event.RequestID,
			AttemptNumber: event.AttemptNumber,
			ResponseID:    event.ResponseID,
			Model:         event.Model,
			TurnID:        event.TurnID,
			Error:         event.Error,
			Usage:         usage,
		})
	}
}

// modelRequestAttemptHook builds the model runner's attempt hook directly so
// compaction, which owns no turn loop, pairs its own hooks (Rust #61562).
func (r *RuntimeRouter) modelRequestAttemptHook(threadID string, sessionID string, cwd string) model.ModelRequestHook {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	sessionID = strings.TrimSpace(sessionID)
	cwd = strings.TrimSpace(cwd)
	return func(ctx context.Context, event model.ModelRequestHookEvent) error {
		return r.runModelRequestHook(ctx, threadID, sessionID, cwd, event)
	}
}

// runModelRequestHook reports one Pre/PostModelRequest milestone. The model
// runner pairs one Pre and one Post per opened response stream and reports the
// sampling kind, stable request id, attempt number and provider response id
// (Rust #61562/#61587).
func (r *RuntimeRouter) runModelRequestHook(ctx context.Context, threadID string, sessionID string, cwd string, event model.ModelRequestHookEvent) error {
	if r == nil || ctx == nil {
		return nil
	}
	hooks := r.hooksForThreadTurn(threadID, cwd)
	if len(hooks) == 0 {
		return nil
	}
	var errorText *string
	if event.Error != "" {
		errorText = &event.Error
	}
	var responseID *string
	if value := strings.TrimSpace(event.ResponseID); value != "" {
		responseID = &value
	}
	billable := event.Phase == "post" &&
		(event.Status == "completed" || (event.Status == "failed" && event.Usage != nil))
	hookResult, hookErr := r.requireHookRunner().RunModelRequest(ctx, &HookModelRequestRequest{
		ThreadID:             threadID,
		SessionID:            sessionID,
		TurnID:               event.TurnID,
		CWD:                  cwd,
		Model:                event.Model,
		Phase:                event.Phase,
		Status:               event.Status,
		Kind:                 string(event.Kind),
		RequestID:            event.RequestID,
		AttemptNumber:        event.AttemptNumber,
		ResponseID:           responseID,
		Error:                errorText,
		TokenUsage:           event.Usage,
		Hooks:                hooks,
		BillableModelRequest: billable,
	})
	if hookErr != nil || hookResult == nil {
		return nil
	}
	return r.applyModelRequestHookCharge(threadID, event.TurnID, hookResult)
}

// applyModelRequestHookCharge posts a billable PostModelRequest run's hook
// price to the thread's shared rollout budget, or to the host's accounting
// when one is installed (Rust #61580/#61958). A bad price surfaces as a
// terminal failure only after its valid siblings were posted; an
// unrepresentable total is refused before anything is posted (#62559).
func (r *RuntimeRouter) applyModelRequestHookCharge(threadID string, turnID string, hookResult *HookRunResult) error {
	if hookResult == nil {
		return nil
	}
	charge := hookResult.ModelRequestHookCharge
	if charge.Overflow {
		// An unrepresentable hook total is refused before any host report, so
		// a partial charge never reaches the account (Rust #62559).
		r.stopNativeBudgetOnOverflow(threadID)
		return fmt.Errorf("%w: %v", model.ErrUsageAccountingFailed, runtimeutil.ErrRolloutHookChargeOverflow)
	}
	if charge.Units != 0 {
		hookCharge := runtimeutil.HookCharge{Units: charge.Units}
		if handled, err := r.reportHookChargeToHost(threadID, turnID, hookCharge); handled {
			if err != nil {
				return err
			}
		} else if budget := r.rolloutBudgetForThread(threadID); budget != nil {
			exhausted, chargeErr := budget.RecordHookCharge(hookCharge)
			if chargeErr != nil {
				return fmt.Errorf("%w: %v", model.ErrUsageAccountingFailed, chargeErr)
			}
			if exhausted {
				r.rolloutBudgetExhausted.Store(true)
			}
		}
	}
	if message := strings.TrimSpace(hookResult.ModelRequestHookInvalidPrice); message != "" {
		return fmt.Errorf("%w: Invalid post-hook budget units: %s", model.ErrUsageAccountingFailed, message)
	}
	return nil
}
