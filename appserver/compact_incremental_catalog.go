package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/model"
	"codex_go/session"
)

// This file ports the position semantics of Rust #51188 (`062439b2f8`, "Record
// base instructions in incremental tool history"),
// `codex-rs/core/src/compact.rs::assemble_compaction_history`: when a compaction
// (or a context reset) rebuilds a context window, the replacement history opens
// with the window prefix - the `additional_tools` catalog followed by the
// base-instruction developer message - chained ahead of the rebuilt
// conversation, which keeps the compaction summary last.
//
// Rust re-derives that prefix from the current session settings
// (`build_initial_context_with_world_state` -> `split_prefix_updates`). Go
// carries forward the prefix the replaced window already recorded in history
// instead: the declarations a window sends are the ones it recorded, and the
// two agree whenever the tool set is stable across the window (the case the
// Rust ordering tests exercise).

// incrementalDeclarationPrefixItems returns the harness-authored declarations
// a context window recorded at its start: the first `additional_tools` catalog
// and the base-instruction developer message that follows it. The window-start
// path places them ahead of the window's first user message
// (`incrementalToolCatalogForTurn`).
//
// Later turns append incremental update notices and replacement declarations
// with the same catalog-kind metadata. Rust #52295 treats a notice as part of
// the batch it precedes, not as a persistent window baseline, so a rebuilt
// prefix stops before the first notice and keeps at most one catalog and one
// base-instruction item.
func incrementalDeclarationPrefixItems(items []session.Item) []session.Item {
	prefix := make([]session.Item, 0, 2)
	sawCatalog := false
	sawInstructions := false
	for i := range items {
		if incrementalDeclarationItemKind(&items[i]) != incrementalToolCatalogKind {
			break
		}
		if incrementalDeclarationContainsKind(&items[i], session.ToolCatalogUpdateHintKind) {
			break
		}
		switch strings.TrimSpace(items[i].Type) {
		case "additional_tools":
			if sawCatalog {
				return prefix
			}
			sawCatalog = true
		case "message":
			if !sawCatalog || sawInstructions ||
				!incrementalDeclarationIsBaseInstructions(&items[i]) {
				return prefix
			}
			sawInstructions = true
		default:
			return prefix
		}
		prefix = append(prefix, items[i])
		if sawCatalog && sawInstructions {
			return prefix
		}
	}
	return prefix
}

func incrementalDeclarationContainsKind(item *session.Item, want string) bool {
	for _, kind := range sessionItemContentItemKinds(item) {
		if kind == want {
			return true
		}
	}
	return false
}

func incrementalDeclarationIsBaseInstructions(item *session.Item) bool {
	kinds := sessionItemContentItemKinds(item)
	if len(kinds) == 0 {
		// Older records did not stamp the base-instruction kind. The item still
		// has to be the first developer message after the initial catalog.
		return sessionItemRole(item) == "developer"
	}
	for _, kind := range kinds {
		if kind != session.BaseInstructionsContentKind {
			return false
		}
	}
	return true
}

// incrementalDeclarationItemKind reads the harness kind the window-start path
// stamps on a declaration entry. The rollout round trip keeps it on
// `metadata.kind` or `data.kind`, so both are consulted.
func incrementalDeclarationItemKind(item *session.Item) string {
	if item == nil {
		return ""
	}
	if kind, ok := item.Data["kind"].(string); ok && strings.TrimSpace(kind) != "" {
		return strings.TrimSpace(kind)
	}
	if kind, ok := item.Metadata["kind"].(string); ok && strings.TrimSpace(kind) != "" {
		return strings.TrimSpace(kind)
	}
	return ""
}

// incrementalDeclarationPrefixParts extracts the catalog definitions and the
// base-instruction text the prefix recorded, so the replacement window can diff
// later turns against the declarations it already sent instead of re-declaring
// (and duplicating) them.
func incrementalDeclarationPrefixParts(prefix []session.Item) ([]any, string, bool) {
	var definitions []any
	instructions := ""
	sawCatalog := false
	for i := range prefix {
		item := &prefix[i]
		switch strings.TrimSpace(item.Type) {
		case "additional_tools":
			object := incrementalDeclarationItemObject(item)
			if object == nil {
				continue
			}
			tools, _ := object["tools"].([]any)
			if len(tools) == 0 {
				continue
			}
			definitions = tools
			sawCatalog = true
		case "message":
			if instructions == "" {
				instructions = strings.TrimSpace(item.Text)
			}
		}
	}
	return definitions, instructions, sawCatalog
}

// incrementalDeclarationItemObject decodes the wire object a declaration entry
// recorded. The raw response item is authoritative; the typed fields are the
// fallback for a record that only kept the structured view.
func incrementalDeclarationItemObject(item *session.Item) map[string]any {
	if item == nil {
		return nil
	}
	if len(item.Raw) > 0 {
		var object map[string]any
		if err := json.Unmarshal(item.Raw, &object); err == nil && object != nil {
			return object
		}
	}
	if len(item.Data) == 0 {
		return nil
	}
	encoded, err := json.Marshal(item.Data)
	if err != nil {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil
	}
	return object
}

// placeIncrementalCatalogBeforeCompactedHistory mirrors Rust #51188
// `compact::assemble_compaction_history` for a rebuilt history: the replaced
// window's recorded declaration prefix is chained ahead of the rebuilt
// conversation, so a cold replay reads the tool catalog before the compaction
// summary and ahead of the window's first user message.
//
// It also records the replacement window's catalog baseline from the carried
// prefix, so the first turn of the new window diffs against declarations that
// are already in history instead of re-deriving and duplicating them, while a
// later declaration change still travels as an incremental update.
func placeIncrementalCatalogBeforeCompactedHistory(record *session.Record, prefix []session.Item, windowNumber uint64, modelID string, instructions *string) error {
	return applyIncrementalCatalogBaseline(record, prefix, windowNumber, modelID, instructions, true)
}

// recordIncrementalCatalogBaseline updates the replacement window's comparison
// snapshots without prepending the declaration prefix. The full compaction
// path installs that prefix as part of the rendered initial context (#51117).
func recordIncrementalCatalogBaseline(record *session.Record, prefix []session.Item, windowNumber uint64, modelID string, instructions *string) error {
	return applyIncrementalCatalogBaseline(record, prefix, windowNumber, modelID, instructions, false)
}

func applyIncrementalCatalogBaseline(record *session.Record, prefix []session.Item, windowNumber uint64, modelID string, instructions *string, prepend bool) error {
	if record == nil {
		return nil
	}
	// Rust #57262: compaction starts a fresh skill catalog listing and resets
	// its metadata budget, so the incremental snapshots are dropped here even
	// when this window carries no declaration prefix.
	if err := clearSkillCatalogSnapshots(record); err != nil {
		return err
	}
	if len(prefix) == 0 {
		return nil
	}
	definitions, recordedInstructions, ok := incrementalDeclarationPrefixParts(prefix)
	if !ok {
		return nil
	}
	if instructions != nil {
		recordedInstructions = *instructions
		prefix = incrementalPrefixWithInstructions(prefix, recordedInstructions)
	}
	if strings.TrimSpace(modelID) == "" {
		modelID = strings.TrimSpace(record.Metadata.Model)
	}
	if prepend {
		record.Items = append(append([]session.Item(nil), prefix...), record.Items...)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return err
	}
	hashes, err := session.TopLevelToolsHashes(definitions)
	if err != nil {
		return err
	}
	toolsSnapshot, err := session.EncodeTopLevelToolsSnapshot(windowNumber, hashes)
	if err != nil {
		return err
	}
	state.TopLevelTools = toolsSnapshot
	if instructions != nil || strings.TrimSpace(recordedInstructions) != "" {
		baseSnapshot, err := session.EncodeModelInstructionsSnapshot(windowNumber, modelID, session.BaseInstructionsHash(recordedInstructions))
		if err != nil {
			return err
		}
		state.BaseInstructions = baseSnapshot
	}
	encoded, err := session.EncodeWorldState(state)
	if err != nil {
		return err
	}
	record.Metadata.WorldState = encoded
	extra := ensureRecordExtra(record.Metadata.Extra)
	extra[model.ToolDeclarationWindowNumberKey] = windowNumber
	extra[model.ToolDeclarationModeKey] = string(model.ToolDeclarationIncremental)
	delete(extra, model.ToolDeclarationItemsKey)
	record.Metadata.Extra = extra
	return nil
}

func incrementalPrefixWithInstructions(prefix []session.Item, instructions string) []session.Item {
	out := make([]session.Item, 0, len(prefix))
	replaced := false
	for i := range prefix {
		item := prefix[i]
		if item.Type != "message" {
			out = append(out, item)
			continue
		}
		if replaced {
			out = append(out, item)
			continue
		}
		replaced = true
		if strings.TrimSpace(instructions) == "" {
			continue
		}
		object := incrementalDeclarationItemObject(&item)
		if object == nil {
			object = map[string]any{
				"type":    "message",
				"role":    "developer",
				"content": []any{},
			}
		}
		object["type"] = "message"
		object["role"] = "developer"
		object["content"] = []any{map[string]any{"type": "input_text", "text": instructions}}
		raw, err := json.Marshal(object)
		if err == nil {
			item.Raw = raw
		}
		item.Text = instructions
		out = append(out, item)
	}
	return out
}
