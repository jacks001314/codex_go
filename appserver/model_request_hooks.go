package appserver

import (
	"context"
	"strings"

	"codex_go/model"
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
	return func(ctx context.Context, event turn.ModelRequestHookEvent) {
		var usage *model.AgentUsage
		if event.Usage != nil {
			value := *event.Usage
			usage = &value
		}
		hook(ctx, model.ModelRequestHookEvent{
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
	return func(ctx context.Context, event model.ModelRequestHookEvent) {
		r.runModelRequestHook(ctx, threadID, sessionID, cwd, event)
	}
}

// runModelRequestHook reports one Pre/PostModelRequest milestone. The model
// runner pairs one Pre and one Post per opened response stream and reports the
// sampling kind, stable request id, attempt number and provider response id
// (Rust #61562/#61587).
func (r *RuntimeRouter) runModelRequestHook(ctx context.Context, threadID string, sessionID string, cwd string, event model.ModelRequestHookEvent) {
	if r == nil || ctx == nil {
		return
	}
	hooks := r.hooksForThreadTurn(threadID, cwd)
	if len(hooks) == 0 {
		return
	}
	var errorText *string
	if event.Error != "" {
		errorText = &event.Error
	}
	var responseID *string
	if value := strings.TrimSpace(event.ResponseID); value != "" {
		responseID = &value
	}
	_, _ = r.requireHookRunner().RunModelRequest(ctx, &HookModelRequestRequest{
		ThreadID:      threadID,
		SessionID:     sessionID,
		TurnID:        event.TurnID,
		CWD:           cwd,
		Model:         event.Model,
		Phase:         event.Phase,
		Status:        event.Status,
		Kind:          string(event.Kind),
		RequestID:     event.RequestID,
		AttemptNumber: event.AttemptNumber,
		ResponseID:    responseID,
		Error:         errorText,
		TokenUsage:    event.Usage,
		Hooks:         hooks,
	})
}
