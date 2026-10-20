package appserver

import (
	"context"
	"strings"

	"codex_go/turn"
)

// modelRequestHookCallback wires the observation-only Pre/PostModelRequest
// events into the thread's discovered hooks (Rust #55144). Hook output is
// reported but never allows a request to be vetoed or its context changed.
func (r *RuntimeRouter) modelRequestHookCallback(threadID string, cwd string) turn.ModelRequestHook {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	cwd = strings.TrimSpace(cwd)
	return func(ctx context.Context, event turn.ModelRequestHookEvent) {
		if r == nil || ctx == nil {
			return
		}
		hooks := r.hooksForCWD(cwd, threadID)
		if len(hooks) == 0 {
			return
		}
		var errorText *string
		if event.Error != "" {
			errorText = &event.Error
		}
		_, _ = r.requireHookRunner().RunModelRequest(ctx, &HookModelRequestRequest{
			ThreadID:   threadID,
			TurnID:     event.TurnID,
			CWD:        cwd,
			Model:      event.Model,
			Phase:      event.Phase,
			Status:     event.Status,
			Error:      errorText,
			TokenUsage: event.Usage,
			Hooks:      hooks,
		})
	}
}
