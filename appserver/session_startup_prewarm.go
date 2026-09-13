package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/model"
)

// Session-startup prewarm mirrors Rust core/src/session_startup_prewarm.rs:
// when a thread is created, an asynchronous websocket prewarm is scheduled so
// the first regular turn reuses the established connection (its response id
// becomes the first request's PreviousResponseID) instead of paying the
// connection latency. The prewarm is token-free (generate=false) and
// best-effort: a turn that starts before the prewarm finishes proceeds without
// it, matching Rust's Unavailable resolution.

const startupPrewarmTimeout = 15 * time.Second

// startupPrewarmState is the per-thread prewarm result.
type startupPrewarmState struct {
	responseID string
	startedAt  time.Time
	finished   bool
	finishedAt time.Time
	// status mirrors Rust's prewarm task status ("ready" when the websocket
	// prewarm returned without error, otherwise "failed").
	status   string
	consumed bool
}

// scheduleStartupPrewarm kicks off an asynchronous websocket prewarm for a
// newly created thread (Rust Session::schedule_startup_prewarm, session.rs),
// called from the ThreadStart lifecycle. Agents that do not implement the
// prewarm interface are skipped (Rust's websocket-disabled path falls back to
// auth prewarm, which Go's auth store resolves lazily on demand).
func (r *RuntimeRouter) scheduleStartupPrewarm(response *ThreadStartResponse) {
	if r == nil || response == nil || response.Thread == nil || r.services.Agent == nil {
		return
	}
	prewarmer, ok := r.services.Agent.(guardianPrewarmer)
	if !ok {
		return
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if threadID == "" {
		return
	}
	r.startupPrewarmMu.Lock()
	if r.startupPrewarms == nil {
		r.startupPrewarms = map[string]*startupPrewarmState{}
	}
	startedAt := time.Now().UTC()
	r.startupPrewarms[threadID] = &startupPrewarmState{startedAt: startedAt}
	r.startupPrewarmMu.Unlock()

	modelID := strings.TrimSpace(response.Model)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), startupPrewarmTimeout)
		defer cancel()
		var responseID string
		status := "failed"
		resp, err := prewarmer.Prewarm(ctx, &model.AgentRequest{
			Model:      modelID,
			Originator: "session_startup",
		})
		if err == nil {
			status = "ready"
		}
		if err == nil && resp != nil {
			responseID = strings.TrimSpace(resp.ResponseID)
		}
		finishedAt := time.Now().UTC()
		r.startupPrewarmMu.Lock()
		if state := r.startupPrewarms[threadID]; state != nil {
			state.responseID = responseID
			state.finished = true
			state.finishedAt = finishedAt
			state.status = status
		}
		r.startupPrewarmMu.Unlock()
		// Rust's prewarm task records these when it completes.
		r.recordStartupPhase("startup_prewarm_total", finishedAt.Sub(startedAt), status)
		r.recordStartupPrewarmDuration(status, finishedAt.Sub(startedAt))
	}()
}

// takeStartupPrewarmResponseID returns and consumes the prewarmed response id
// for the thread when the prewarm finished before the turn started; otherwise
// it returns "" and the turn proceeds without the prewarm (Rust
// consume_startup_prewarm_for_regular_turn -> Unavailable).
func (r *RuntimeRouter) takeStartupPrewarmResponseID(threadID string) string {
	if r == nil {
		return ""
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	r.startupPrewarmMu.Lock()
	state := r.startupPrewarms[threadID]
	if state == nil || !state.finished || state.consumed {
		r.startupPrewarmMu.Unlock()
		return ""
	}
	responseID := strings.TrimSpace(state.responseID)
	age := time.Since(state.startedAt)
	if age < 0 {
		age = 0
	}
	if responseID == "" {
		// The prewarm finished without a usable response id; Rust reports the
		// prewarm setup failure as unavailable with status "failed".
		r.startupPrewarmMu.Unlock()
		r.recordStartupPrewarmAgeAtFirstTurn("failed", age)
		return ""
	}
	state.consumed = true
	r.startupPrewarmMu.Unlock()
	r.recordStartupPrewarmAgeAtFirstTurn("consumed", age)
	return responseID
}

// startupPrewarmsSnapshot returns a copy of the prewarm states for tests and
// diagnostics.
func (r *RuntimeRouter) startupPrewarmsSnapshot() map[string]*startupPrewarmState {
	out := map[string]*startupPrewarmState{}
	if r == nil {
		return out
	}
	r.startupPrewarmMu.Lock()
	defer r.startupPrewarmMu.Unlock()
	for threadID, state := range r.startupPrewarms {
		clone := *state
		out[threadID] = &clone
	}
	return out
}
