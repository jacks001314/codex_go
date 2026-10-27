package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/features"
	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

// Rust parity: core/src/session/mod.rs and core/src/session/world_state.rs
// (#57287, "Preserve parent tool declarations in prediction forks").
//
// A prediction fork or thread sample may disable MCP when the inherited context
// window already declares a Responses Lite tool catalog. The child keeps the
// parent's inherited declarations instead of sending a new catalog, and a
// developer notice tells the model that MCP tools are unavailable.

const (
	// predictionMCPServersDisabledKey marks an ephemeral record whose MCP
	// projection must be empty (Rust `McpServersDisabled`).
	predictionMCPServersDisabledKey = "mcp_servers_disabled"
	// predictionMCPUnavailableNotice is Rust's DeveloperInstructions payload.
	predictionMCPUnavailableNotice = "<mcp_tools>\nMCP tools are unavailable for this request.\n</mcp_tools>"
	// predictionMCPUnavailableNoticeKind classifies the recorded notice.
	predictionMCPUnavailableNoticeKind = "mcp_tools.unavailable"
)

// predictionForkDisablesMCP mirrors Rust thread_processor.rs: disabling MCP only
// applies to a prediction fork whose inherited history carries an
// `additional_tools` catalog and whose model uses Responses Lite.
func (r *RuntimeRouter) predictionForkDisablesMCP(source, fork *session.Record) bool {
	if r == nil || source == nil || fork == nil {
		return false
	}
	if !runtimeSessionRecordHasAdditionalTools(source) {
		return false
	}
	modelID := firstNonEmpty(strings.TrimSpace(fork.Metadata.Model), strings.TrimSpace(source.Metadata.Model))
	info := r.modelInfoForRuntime(modelID)
	return info != nil && info.UseResponsesLite
}

// runtimeSessionRecordHasAdditionalTools reports whether the record's history
// already declares a Responses Lite tool catalog (Rust
// `ResponseItem::AdditionalTools`).
func runtimeSessionRecordHasAdditionalTools(record *session.Record) bool {
	if record == nil {
		return false
	}
	for i := range record.Items {
		if strings.TrimSpace(record.Items[i].Type) == "additional_tools" {
			return true
		}
	}
	return false
}

// historyInputItemsHaveAdditionalTools reports whether request-shaped history
// already declares a Responses Lite tool catalog (Rust
// `ResponseItem::AdditionalTools`). thread/sample reads the source record
// without items, so it checks the loaded history instead.
func historyInputItemsHaveAdditionalTools(items []any) bool {
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringFromAny(object["type"])) == "additional_tools" {
			return true
		}
	}
	return false
}

// predictionPreserveParentToolsEnabled resolves the inherited feature setting
// for the fork (Rust `Session::preserve_parent_tool_declarations`).
func (r *RuntimeRouter) predictionPreserveParentToolsEnabled(record *session.Record) bool {
	if r == nil || record == nil {
		return false
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{
		ThreadID: string(record.ID),
		CWD:      record.Metadata.CWD,
		Model:    record.Metadata.Model,
		Config:   threadRecordConfigOverrides(record),
	})
	if err != nil || cfg == nil {
		return false
	}
	return features.Enabled(cfg.FeatureSettings(), "prediction_preserve_parent_tools")
}

// predictionMCPUnavailableNoticeInputItem is the request-shape form of the
// notice used by thread/sample, which does not materialize an ephemeral record.
func predictionMCPUnavailableNoticeInputItem() map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": predictionMCPUnavailableNotice}},
	}
}

// predictionMCPUnavailableNoticeItem builds the harness-authored developer
// notice recorded on the fork (Rust `DeveloperInstructions::new(...)`).
func predictionMCPUnavailableNoticeItem(createdAt time.Time) session.Item {
	raw, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": predictionMCPUnavailableNotice}},
	})
	if err != nil {
		raw = nil
	}
	hidden := map[string]any{
		"kind":             predictionMCPUnavailableNoticeKind,
		"hiddenFromThread": true,
	}
	return session.Item{
		ID:        "mcp-tools-unavailable",
		Type:      "message",
		Role:      "developer",
		Text:      predictionMCPUnavailableNotice,
		Content:   []session.ContentPart{{Type: "input_text", Text: predictionMCPUnavailableNotice}},
		CreatedAt: createdAt,
		Data:      cloneAnyMap(hidden),
		Metadata:  cloneAnyMap(hidden),
		Raw:       raw,
	}
}

// threadMCPServersDisabled reports whether the thread's MCP projection is
// disabled. Only ephemeral threads (prediction forks) can carry the marker.
func (r *RuntimeRouter) threadMCPServersDisabled(threadID string) bool {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return false
	}
	record, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false)
	if !ok || record == nil {
		return false
	}
	return boolFromMap(record.Metadata.Extra, predictionMCPServersDisabledKey)
}

// projectionWithoutMCPServers mirrors Rust `runtime_config_with_context` when
// the thread carries `McpServersDisabled`: no servers, catalog, apps, or plugin
// contributions are projected.
func projectionWithoutMCPServers(base *mcp.RuntimeConfig) *mcp.RuntimeConfig {
	if base == nil {
		return nil
	}
	base.Servers = map[string]mcp.ServerRegistration{}
	base.AppsEnabled = false
	base.ConnectorIDs = nil
	return base
}
