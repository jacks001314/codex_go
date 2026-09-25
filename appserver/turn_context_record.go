package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// recordRuntimeTurnContext persists the thread's per-turn context record and
// keeps the recovered previous-turn settings in step.
//
// Rust writes one `TurnContextItem` per real user turn ("so resume/lazy replay
// can recover the latest durable baseline"), carrying the model and the model's
// compaction compatibility hash. Its rollout reconstruction then rebuilds
// `PreviousTurnSettings` from the last record, which is what the previous-model
// compaction decision compares against (#46324). Go previously wrote no such
// record, so a thread had no recorded previous turn at all.
func (r *RuntimeRouter) recordRuntimeTurnContext(threadID string, turnID string, runConfig *appTurnRunConfig, record *session.Record) {
	if r == nil || runConfig == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return
	}
	cwd := ""
	if record != nil {
		cwd = strings.TrimSpace(record.Metadata.CWD)
	}
	turnContext := rollout.TurnContextRecord{
		TurnID:         strings.TrimSpace(turnID),
		CWD:            cwd,
		ApprovalPolicy: strings.TrimSpace(runConfig.ApprovalPolicy),
		SandboxPolicy:  nil,
		Effort:         strings.TrimSpace(runConfig.ReasoningEffort),
		Personality:    strings.TrimSpace(runConfig.Personality),
		Model:          strings.TrimSpace(runConfig.Model),
		CompHash:       strings.TrimSpace(r.modelCompHash(runConfig.Model)),
		// The selected program is persisted with the model so resume/fork replay
		// rebuilds the model/program pair the turn used (Rust #48224).
		CyberAccessProgram: strings.TrimSpace(runConfig.CyberAccessProgram),
	}
	if strings.TrimSpace(runConfig.SandboxPolicy) != "" {
		turnContext.SandboxPolicy = runConfig.SandboxPolicy
	}
	if strings.TrimSpace(turnContext.Model) == "" {
		return
	}
	payload, err := json.Marshal(turnContext)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	_ = r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return recorder.AppendTurnContext(turnContext, now)
	})
	r.setRuntimeTurnContext(threadID, payload)
}

// modelCompHash returns the model's compaction compatibility hash (Rust
// `ModelInfo::comp_hash`).
func (r *RuntimeRouter) modelCompHash(modelID string) string {
	if r == nil {
		return ""
	}
	info := r.modelInfoForRuntime(strings.TrimSpace(modelID))
	if info == nil {
		return ""
	}
	return strings.TrimSpace(info.CompHash)
}

// setRuntimeTurnContext records the thread's latest turn-context payload.
func (r *RuntimeRouter) setRuntimeTurnContext(threadID string, payload json.RawMessage) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" || len(payload) == 0 {
		return
	}
	r.turnContextMu.Lock()
	defer r.turnContextMu.Unlock()
	if r.turnContexts == nil {
		r.turnContexts = map[string]json.RawMessage{}
	}
	r.turnContexts[threadID] = append(json.RawMessage(nil), payload...)
}

// runtimePreviousTurnSettings returns the model, compaction compatibility hash
// and cyber access program recorded by the thread's previous turn (Rust
// `sess.previous_turn_settings()`).
//
// A live thread answers from the payload recorded when its last turn started; a
// thread that has not run yet in this process is seeded from the
// rollout-reconstructed record, so a resumed thread compares against the model
// its rollout was recorded with. ok is false when no turn context is known,
// which matches Rust returning `None` for a brand-new thread.
func (r *RuntimeRouter) runtimePreviousTurnSettings(threadID string, record *session.Record) (string, string, string, bool) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return "", "", "", false
	}
	r.turnContextMu.Lock()
	raw := r.turnContexts[threadID]
	if len(raw) == 0 && record != nil && len(record.Metadata.TurnContext) > 0 {
		raw = append(json.RawMessage(nil), record.Metadata.TurnContext...)
		if r.turnContexts == nil {
			r.turnContexts = map[string]json.RawMessage{}
		}
		r.turnContexts[threadID] = raw
	}
	r.turnContextMu.Unlock()
	return rollout.TurnContextSettings(raw)
}
