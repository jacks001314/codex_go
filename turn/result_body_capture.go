package turn

import (
	"codex_go/model"
)

// McpResultBodyCapture carries the per-turn permission to capture Apps result
// bodies from direct MCP calls (Rust ExecutedToolCalls::capture_mcp_result,
// #62392). The turn runtime resolves it once, from the feature flag and the
// account that will serve the turn, so every call in the turn is judged against
// the same credential instance.
type McpResultBodyCapture struct {
	// Enabled is features.connector_tool_result_passthrough. Nothing is captured
	// while the feature stays off.
	Enabled bool
	// SessionAuthMode is the turn's credential class. Only a ChatGPT runtime
	// credential may supply result bodies to its own backend.
	SessionAuthMode string
	// RuntimeAuth describes the ChatGPT Codex backend binding the MCP client
	// presents, when the turn has one.
	RuntimeAuth *RuntimeAuthBinding
}

// RuntimeAuthBinding mirrors the runtime credential binding the MCP layer
// reports for a server (Rust rmcp TransportRecipe's runtime auth provider).
type RuntimeAuthBinding struct {
	UsesCodexBackend bool
}

// resultBodyTurnFor returns the store that captures for this recorder, creating
// it on first use for the given turn and owner. A store from another turn is
// replaced, mirroring Rust's per-task ResultBodyTurn.
func (r *ExecutedToolCallRecorder) resultBodyTurnFor(turnID string, owner string) *ResultBodyTurn {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resultBodyTurn != nil && r.resultBodyTurn.TurnID() == turnID && r.resultBodyTurn.Owner() == owner {
		return r.resultBodyTurn
	}
	if turnID == "" {
		return nil
	}
	r.resultBodyTurn = NewResultBodyTurn(turnID, owner)
	return r.resultBodyTurn
}

// BeginResultBodyTurn starts the bounded result-body store for one turn and
// records the credential instance that owns it. Passing an empty auth owner or a
// disabled capture drops any previous store, so private bytes never survive the
// turn that captured them.
func (r *ExecutedToolCallRecorder) BeginResultBodyTurn(turnID string, owner string, captures bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.capturesResultBodies = captures
	if !captures || turnID == "" || owner == "" {
		r.resultBodyTurn = nil
		return
	}
	r.resultBodyTurn = NewResultBodyTurn(turnID, owner)
}

// SetIncludeGeneralMetadata records whether ordinary executed-tool metadata is
// enabled for this recorder. Applying it also keeps the recorder alive for a
// connector-only capture (Rust refresh, #62392).
func (r *ExecutedToolCallRecorder) SetIncludeGeneralMetadata(include bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.includeGeneralMetadata = include
}

// IncludeGeneralMetadata reports whether ordinary executed-tool metadata is
// enabled, which decides whether a request needs the connector-only projection.
func (r *ExecutedToolCallRecorder) IncludeGeneralMetadata() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.includeGeneralMetadata
}

// CapturesResultBodies reports whether the recorder keeps connector result
// bodies for the current turn.
func (r *ExecutedToolCallRecorder) CapturesResultBodies() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.capturesResultBodies
}

// ResultBodyOwnerForTurn reports the credential instance that owns the current
// turn's result bodies, or "" when this turn captures none.
func (r *ExecutedToolCallRecorder) ResultBodyOwnerForTurn(turnID string) string {
	if r == nil || turnID == "" {
		return ""
	}
	r.mu.Lock()
	store := r.resultBodyTurn
	r.mu.Unlock()
	if store == nil || store.TurnID() != turnID {
		return ""
	}
	return store.Owner()
}

// CaptureResultBody records one completed direct MCP result and returns the
// local ID its executed-tool row should carry. It reports false when the feature
// is off, the turn has no store, or the result is not replayable.
func (r *ExecutedToolCallRecorder) CaptureResultBody(turnID string, owner string, source model.ToolResultBodySource) (string, bool) {
	if r == nil {
		return "", false
	}
	store := r.resultBodyTurnFor(turnID, owner)
	if store == nil {
		return "", false
	}
	return store.Record(source)
}

// DiscardResultBody releases a captured body whose row left the request input.
func (r *ExecutedToolCallRecorder) DiscardResultBody(turnID string, owner string, id string) {
	if r == nil || id == "" {
		return
	}
	if store := r.resultBodyTurnFor(turnID, owner); store != nil {
		store.Discard(id)
	}
}

// ResultBodyAttachments selects the retained bodies for this request input. The
// owner must match the credential instance that captured them, so a turn served
// by a different account attaches nothing (Rust ResultBodyTurn::for_input).
func (r *ExecutedToolCallRecorder) ResultBodyAttachments(owner string, input []any) []model.ToolResultAttachment {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	store := r.resultBodyTurn
	r.mu.Unlock()
	if store == nil || store.Owner() != owner {
		return nil
	}
	return store.Attachments(input)
}

// ScopeToolResultsForInput applies the connector-only projection when ordinary
// executed-tool metadata is disabled. Rust keeps the full metadata otherwise, and
// also when passthrough is off.
func (r *ExecutedToolCallRecorder) ScopeToolResultsForInput(input []any) []any {
	if r == nil || !r.CapturesResultBodies() || r.IncludeGeneralMetadata() {
		return input
	}
	return model.ScopeExecutedToolCallsForRequest(input)
}
