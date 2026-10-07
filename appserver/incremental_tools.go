package appserver

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// This file wires the Responses Lite incremental tool catalog into the turn
// boundary (Rust `codex-rs/core/src/context/world_state/top_level_tools.rs`,
// #50540 `Send incremental tool catalog updates in Responses Lite`, plus
// #51188 base instructions and #51202/#51119 removal and namespace update
// notices).
//
// With `incremental_tools` enabled for a Responses Lite model a context window
// records its catalog once at window start - an `additional_tools` item and the
// base-instruction developer message - as ordinary history ahead of the
// window's first user message. Later turns of the same window append only added
// or changed declarations, plus developer notices for removals, so the
// declarations already sent keep their position and the request prefix stays
// byte-identical. A window replacement (compaction or a context reset)
// re-derives the full catalog from the current settings, exactly like a fresh
// thread.

// incrementalToolCatalogKind is the harness metadata kind recorded on the
// history entries the incremental catalog writes.
const incrementalToolCatalogKind = "tools.incremental_catalog"

// incrementalToolCatalogResult reports what the incremental catalog path
// produced for one turn.
type incrementalToolCatalogResult struct {
	// Active reports that this turn belongs to an incremental tools window, so
	// the caller must not fall back to the frozen declaration prefix.
	Active bool
	// Items are the declarations or updates this turn adds to the request.
	Items []any
	// Prefix reports Items open the window and must therefore be placed ahead of
	// the existing input, mirroring the window start of Rust #50540/#51188.
	Prefix bool
	// SessionItems carries Items as harness-authored history entries, persisted
	// when the window opens so later turns replay them from history instead of
	// re-declaring them.
	SessionItems []session.Item
}

// incrementalToolsEnabledForTurn mirrors Rust
// `StepContext::incremental_tools_enabled`: the feature is opt-in and only
// applies to Responses Lite models.
func incrementalToolsEnabledForTurn(cfg *config.Config, info *model.ModelInfo) bool {
	if cfg == nil || info == nil || !info.UseResponsesLite {
		return false
	}
	return features.Enabled(cfg.FeatureSettings(), "incremental_tools")
}

// responsesLiteToolDeclarations resolves the serialized Responses Lite tool
// catalog for a turn: the model-visible tool definitions, which is the value
// Rust sends through `create_tools_json_for_responses_lite(&model_visible_specs())`.
// hostedTools is the pre-#50540 fallback used when no turn runtime is available
// (tests that build the appserver config without a runtime).
func responsesLiteToolDeclarations(turnRuntime *turn.Runtime, toolMode string, disableCodeModeFallback bool, hostedTools []any) []any {
	if turnRuntime != nil {
		if definitions := turnRuntime.ModelVisibleToolDefinitions(toolMode, disableCodeModeFallback); definitions != nil {
			return definitions
		}
	}
	return hostedTools
}

// incrementalToolCatalogForTurn diffs the turn's catalog against the catalog
// recorded for the thread's current context window and returns the updates to
// add to the request (Rust `build_world_state_for_step` + `render_diff`).
//
// The result is inactive when the thread's current window is not an incremental
// tools window: a non-lite model, a window recorded before this feature existed
// (Rust legacy resume), or an existing window whose catalog was never
// snapshotted. The caller then keeps the pre-existing declaration behavior.
func (r *RuntimeRouter) incrementalToolCatalogForTurn(threadID string, historyItems []any, definitions []any, instructions string, turnID string, createdAt time.Time) (incrementalToolCatalogResult, error) {
	if r == nil || r.services.ThreadRouter == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return incrementalToolCatalogResult{}, nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return incrementalToolCatalogResult{}, err
	}
	windowNumber := r.windowNumberForThread(threadID)
	extra := cloneAnyMap(record.Metadata.Extra)
	// Rust #51480 `current_window_uses_incremental_tools`: a window replacement
	// or a fresh thread adopts the current setting, an existing window keeps
	// whatever it recorded.
	mode, _, changed := model.ResolveToolDeclarationWindow(extra, historyItems, windowNumber, true, true)
	if mode != model.ToolDeclarationIncremental {
		return incrementalToolCatalogResult{}, nil
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return incrementalToolCatalogResult{}, err
	}
	previousHashes, previousKnown := session.TopLevelToolsSnapshotForWindow(state.TopLevelTools, windowNumber)
	if !changed && !previousKnown {
		// An existing window that never recorded a catalog snapshot (a window
		// whose declarations were rebuilt on every request, Rust legacy resume)
		// keeps that shape: only a window boundary starts a recorded catalog.
		return incrementalToolCatalogResult{}, nil
	}
	previousBaseHash, baseKnown := session.BaseInstructionsSnapshotForWindow(state.BaseInstructions, windowNumber)

	hashes, toolUpdates, err := session.TopLevelToolsUpdates(definitions, previousHashes, previousKnown)
	if err != nil {
		return incrementalToolCatalogResult{}, err
	}
	baseHash, baseUpdates := session.BaseInstructionsUpdate(instructions, baseKnown)
	items := session.MergeWorldStateUpdates(append(toolUpdates, baseUpdates...))
	for i := range items {
		if identified, ok := incrementalToolCatalogRequestItem(items[i], threadID); ok {
			items[i] = identified
		}
	}

	result := incrementalToolCatalogResult{Active: true, Items: items, Prefix: changed}
	for i := range items {
		if item, ok := incrementalToolCatalogSessionItem(turnID, i, items[i], createdAt); ok {
			result.SessionItems = append(result.SessionItems, item)
		}
	}

	updated := false
	if changed || !previousKnown || !sameToolDeclarationHashes(hashes, previousHashes) {
		snapshot, encodeErr := session.EncodeTopLevelToolsSnapshot(windowNumber, hashes)
		if encodeErr != nil {
			return incrementalToolCatalogResult{}, encodeErr
		}
		state.TopLevelTools = snapshot
		updated = true
	}
	if changed || !baseKnown || baseHash != previousBaseHash {
		snapshot, encodeErr := session.EncodeBaseInstructionsSnapshot(windowNumber, baseHash)
		if encodeErr != nil {
			return incrementalToolCatalogResult{}, encodeErr
		}
		state.BaseInstructions = snapshot
		updated = true
	}
	if changed {
		// Freeze the window decision so a resumed window keeps its catalog even
		// when the feature is later disabled (Rust #51480). The declarations
		// themselves live in history, so no prefix is frozen for replay.
		if extra == nil {
			extra = map[string]any{}
		}
		extra[model.ToolDeclarationWindowNumberKey] = windowNumber
		extra[model.ToolDeclarationModeKey] = string(model.ToolDeclarationIncremental)
		delete(extra, model.ToolDeclarationItemsKey)
		record.Metadata.Extra = extra
		updated = true
	}
	if len(result.SessionItems) > 0 && result.Prefix {
		// A window replacement rebuilds the history, and the re-derived prefix
		// opens it (Rust #51188 `assemble_compaction_history`: the tool
		// declarations and the base-instruction developer message are chained
		// ahead of the rebuilt history, which keeps the compaction summary
		// last). Appending instead would place the catalog behind the summary,
		// so the next turn would read the declarations after it even though the
		// request that opened the window sent them first.
		record.Items = append(append([]session.Item(nil), result.SessionItems...), record.Items...)
		if record.UpdatedAt.Before(createdAt) {
			record.UpdatedAt = createdAt
		}
		if record.RecencyAt.Before(createdAt) {
			record.RecencyAt = createdAt
		}
		updated = true
	}
	if updated {
		encoded, encodeErr := session.EncodeWorldState(state)
		if encodeErr != nil {
			return incrementalToolCatalogResult{}, encodeErr
		}
		record.Metadata.WorldState = encoded
		if saveErr := r.runtimeSaveThreadRecord(record); saveErr != nil {
			return incrementalToolCatalogResult{}, saveErr
		}
	}
	if len(result.SessionItems) > 0 && !result.Prefix {
		// A later turn of a live window only appends added or changed
		// declarations, so the ones already sent keep their position (Rust
		// `record_context_updates_and_set_reference_context_item`).
		if _, appendErr := r.runtimeAppendItems(session.ThreadID(threadID), result.SessionItems); appendErr != nil {
			return incrementalToolCatalogResult{}, appendErr
		}
	}
	if len(result.SessionItems) > 0 {
		_ = r.appendRuntimeRollout(threadID, result.SessionItems, createdAt)
	}
	return result, nil
}

// incrementalToolCatalogRequestItem stamps the deterministic id a responses-lite
// prefix item carries (Rust client.rs #40962 derives it from the thread id and
// the serialized payload), so re-sending an unchanged declaration keeps its
// identity and the server-side prefix cache stays warm.
func incrementalToolCatalogRequestItem(input any, threadID string) (any, bool) {
	object, ok := input.(map[string]any)
	if !ok || object == nil {
		return input, false
	}
	switch strings.TrimSpace(stringFromAny(object["type"])) {
	case "additional_tools":
		payload, err := json.Marshal(object["tools"])
		if err != nil {
			return input, false
		}
		object["id"] = model.ResponsesLiteItemID("at", payload, threadID)
		return object, true
	case "message":
		text := strings.TrimSpace(textFromInputItemContent(object["content"]))
		if text == "" {
			return input, false
		}
		object["id"] = model.ResponsesLiteItemID("msg", []byte(text), threadID)
		return object, true
	default:
		return input, false
	}
}

// incrementalToolCatalogSessionItem converts a world-state request item into the
// history entry that records it, mirroring the other harness-authored world
// state entries (`realtimeWorldStateSessionItemForTurn`): the raw response item
// is preserved byte for byte and the entry stays hidden from the thread view.
func incrementalToolCatalogSessionItem(turnID string, index int, input any, createdAt time.Time) (session.Item, bool) {
	object, ok := input.(map[string]any)
	if !ok || object == nil {
		return session.Item{}, false
	}
	itemType := strings.TrimSpace(stringFromAny(object["type"]))
	switch itemType {
	case "additional_tools", "message":
	default:
		return session.Item{}, false
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return session.Item{}, false
	}
	role := strings.TrimSpace(stringFromAny(object["role"]))
	text := strings.TrimSpace(textFromInputItemContent(object["content"]))
	hidden := map[string]any{
		"kind":             incrementalToolCatalogKind,
		"hiddenFromThread": true,
	}
	return session.Item{
		ID:        "tool-catalog-" + safeIdentifier(turnID) + "-" + strconv.Itoa(index),
		Type:      itemType,
		Role:      role,
		Text:      text,
		CreatedAt: createdAt,
		Data:      map[string]any{"kind": incrementalToolCatalogKind, "hiddenFromThread": true},
		Metadata:  appTurnMetadata(turnID, hidden),
		Raw:       encoded,
	}, true
}

// sameToolDeclarationHashes reports whether two catalog hash maps agree.
func sameToolDeclarationHashes(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, hash := range left {
		if right[name] != hash {
			return false
		}
	}
	return true
}
