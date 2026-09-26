package turn

import (
	"sync"

	"codex_go/retainedctx"
)

// Rust parity: codex-rs/core/src/tools/executed_tool_calls/mcp_attribution.rs.
// Cumulative MCP result attribution, independent of the best-effort call
// recorder: the checkpoint restores across resume and fork, is persisted with a
// response item's harness metadata, and stays model-invisible.

// McpAttributionRecorder mirrors Rust's `McpAttributionRecorder`: one thread's
// cumulative attribution plus the revision bookkeeping a checkpoint
// acknowledgement uses.
type McpAttributionRecorder struct {
	mu    sync.Mutex
	state mcpAttributionState
}

type mcpAttributionState struct {
	attribution       retainedctx.McpAttribution
	revision          uint64
	persistedRevision uint64
}

// mcpAttributionSourceIdentity is Rust's `SourceIdentity`: the dedup key that
// excludes the first turn, so the earliest observed turn is kept.
type mcpAttributionSourceIdentity struct {
	connectorID *string
	pluginID    *string
	serverName  string
	toolName    string
}

// NewMcpAttributionRecorder mirrors `McpAttributionRecorder::new`. Rust seeds
// from `InitialHistory`'s response items and compaction replacement histories;
// Go resolves the same checkpoints from durable history, so `fresh` stands in
// for `InitialHistory::New`/`Cleared` (an empty history where a missing
// checkpoint is expected) and `checkpoints` are the persisted metadata
// checkpoints in rollout order. Pre-attribution history cannot establish that
// earlier context was MCP-free, so its absence marks the recorder's error.
func NewMcpAttributionRecorder(fresh bool, checkpoints []retainedctx.McpAttribution) *McpAttributionRecorder {
	recorder := &McpAttributionRecorder{state: mcpAttributionState{
		// Rust's `McpAttribution::default()` is `status: None, sources: vec![]`.
		// Go's zero value is neither, so the recorder seeds them explicitly.
		attribution: retainedctx.McpAttribution{
			Status:  retainedctx.McpAttributionStatusNone,
			Sources: []retainedctx.McpAttributionSource{},
		},
		// Persist an initial checkpoint even when no MCP result has been recorded.
		revision: 1,
	}}
	foundCheckpoint := fresh
	for i := range checkpoints {
		foundCheckpoint = true
		recorder.state.mergeCheckpoint(&checkpoints[i])
	}
	if !foundCheckpoint {
		recorder.state.markError(retainedctx.McpAttributionErrorHistoryMissingCheckpoint)
	}
	return recorder
}

// Record observes one completed MCP tools/call source.
func (r *McpAttributionRecorder) Record(source retainedctx.McpAttributionSource) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.record(cloneMcpAttributionSource(source), false)
}

// Snapshot returns the cumulative attribution without changing the revision.
func (r *McpAttributionRecorder) Snapshot() retainedctx.McpAttribution {
	if r == nil {
		return retainedctx.McpAttribution{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneMcpAttribution(r.state.attribution)
}

// Checkpoint returns the attribution to persist together with its revision.
// `force` writes an unchanged checkpoint (Rust's forced compaction checkpoint);
// otherwise an acknowledged checkpoint yields nothing.
func (r *McpAttributionRecorder) Checkpoint(force bool) (retainedctx.McpAttribution, uint64, bool) {
	if r == nil {
		return retainedctx.McpAttribution{}, 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !force && r.state.revision == r.state.persistedRevision {
		return retainedctx.McpAttribution{}, 0, false
	}
	return cloneMcpAttribution(r.state.attribution), r.state.revision, true
}

// MarkPersisted acknowledges a checkpoint revision without clearing newer
// changes: an older acknowledgement never advances past the current revision.
func (r *McpAttributionRecorder) MarkPersisted(revision uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	acknowledged := revision
	if acknowledged > r.state.revision {
		acknowledged = r.state.revision
	}
	if acknowledged > r.state.persistedRevision {
		r.state.persistedRevision = acknowledged
	}
}

// untouched reports whether the recorder still holds only its initial state, so
// a lazy history seed can safely replace it. Any recorded source or error
// increments the revision past its initial value.
func (r *McpAttributionRecorder) untouched() bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.revision == 1 && len(r.state.attribution.Sources) == 0 &&
		r.state.attribution.Status == retainedctx.McpAttributionStatusNone
}

// markError records the first error only: a later error or success never
// replaces the reason that explains why the checkpoint stopped being complete.
func (s *mcpAttributionState) markError(reason retainedctx.McpAttributionErrorReason) {
	if s.attribution.Status == retainedctx.McpAttributionStatusAttributionError {
		return
	}
	s.attribution.Status = retainedctx.McpAttributionStatusAttributionError
	s.attribution.ErrorReason = &reason
	s.revision++
}

// mergeCheckpoint restores one persisted checkpoint: an error checkpoint keeps
// its reason (an error without one becomes `restored_error_unknown`), a
// checkpoint whose status and sources disagree is invalid, and each source is
// recorded as a restored observation so a first-turn conflict is reported.
func (s *mcpAttributionState) mergeCheckpoint(checkpoint *retainedctx.McpAttribution) {
	if checkpoint == nil {
		return
	}
	switch {
	case checkpoint.Status == retainedctx.McpAttributionStatusAttributionError:
		reason := retainedctx.McpAttributionErrorRestoredErrorUnknown
		if checkpoint.ErrorReason != nil {
			reason = *checkpoint.ErrorReason
		}
		s.markError(reason)
	case (checkpoint.Status == retainedctx.McpAttributionStatusNone && len(checkpoint.Sources) > 0) ||
		(checkpoint.Status == retainedctx.McpAttributionStatusComplete && len(checkpoint.Sources) == 0):
		s.markError(retainedctx.McpAttributionErrorCheckpointInvalid)
	}
	for _, source := range checkpoint.Sources {
		s.record(cloneMcpAttributionSource(source), true)
	}
}

// record adds a source the first time its identity is observed. A restored
// source whose first turn differs from the recorded one is a conflict, and a
// source missing its identity is invalid; neither changes an existing record.
func (s *mcpAttributionState) record(source retainedctx.McpAttributionSource, restoring bool) {
	identity := identityOfMcpAttributionSource(source)
	for i := range s.attribution.Sources {
		if mcpAttributionIdentitiesEqual(identityOfMcpAttributionSource(s.attribution.Sources[i]), identity) {
			if restoring && s.attribution.Sources[i].FirstTurnID != source.FirstTurnID {
				s.markError(retainedctx.McpAttributionErrorCheckpointSourceConflict)
			}
			return
		}
	}
	if source.ServerName == "" || source.ToolName == "" || source.FirstTurnID == "" {
		if restoring {
			s.markError(retainedctx.McpAttributionErrorCheckpointInvalid)
		} else {
			s.markError(retainedctx.McpAttributionErrorSourceInvalid)
		}
		return
	}
	if s.attribution.Status == retainedctx.McpAttributionStatusNone {
		s.attribution.Status = retainedctx.McpAttributionStatusComplete
	}
	s.attribution.Sources = append(s.attribution.Sources, source)
	s.revision++
}

// identityOfMcpAttributionSource projects the dedup key. `plugin_id` and
// `connector_id` compare by value, and the first turn is excluded.
func identityOfMcpAttributionSource(source retainedctx.McpAttributionSource) mcpAttributionSourceIdentity {
	return mcpAttributionSourceIdentity{
		connectorID: source.ConnectorID,
		pluginID:    source.PluginID,
		serverName:  source.ServerName,
		toolName:    source.ToolName,
	}
}

// mcpAttributionIdentitiesEqual compares the optional identifiers by value
// (Rust's `Option<&str>` equality), not by pointer identity.
func mcpAttributionIdentitiesEqual(a, b mcpAttributionSourceIdentity) bool {
	return equalStringPointers(a.connectorID, b.connectorID) &&
		equalStringPointers(a.pluginID, b.pluginID) &&
		a.serverName == b.serverName &&
		a.toolName == b.toolName
}

func equalStringPointers(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// cloneMcpAttributionSource copies the source and its optional identifiers so a
// recorded source never aliases the caller's memory (Rust clones).
func cloneMcpAttributionSource(source retainedctx.McpAttributionSource) retainedctx.McpAttributionSource {
	return retainedctx.McpAttributionSource{
		ConnectorID: cloneStringPointer(source.ConnectorID),
		PluginID:    cloneStringPointer(source.PluginID),
		ServerName:  source.ServerName,
		ToolName:    source.ToolName,
		FirstTurnID: source.FirstTurnID,
	}
}

// cloneMcpAttribution copies a snapshot's mutable state.
func cloneMcpAttribution(attribution retainedctx.McpAttribution) retainedctx.McpAttribution {
	cloned := retainedctx.McpAttribution{
		Status:  attribution.Status,
		Sources: make([]retainedctx.McpAttributionSource, 0, len(attribution.Sources)),
	}
	if attribution.ErrorReason != nil {
		reason := *attribution.ErrorReason
		cloned.ErrorReason = &reason
	}
	for _, source := range attribution.Sources {
		cloned.Sources = append(cloned.Sources, cloneMcpAttributionSource(source))
	}
	return cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
