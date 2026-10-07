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

// incrementalDeclarationPrefixItems returns the leading run of harness-authored
// declaration items a context window recorded at its start: the
// `additional_tools` catalog and the base-instruction developer message. The
// window-start path places them ahead of the window's first user message
// (`incrementalToolCatalogForTurn`), and a later turn's incremental updates
// append after them, so a leading-run scan yields exactly the window prefix.
func incrementalDeclarationPrefixItems(items []session.Item) []session.Item {
	prefix := make([]session.Item, 0, 2)
	for i := range items {
		if incrementalDeclarationItemKind(&items[i]) != incrementalToolCatalogKind {
			break
		}
		prefix = append(prefix, items[i])
	}
	return prefix
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
func placeIncrementalCatalogBeforeCompactedHistory(record *session.Record, prefix []session.Item, windowNumber uint64) error {
	if record == nil || len(prefix) == 0 {
		return nil
	}
	definitions, instructions, ok := incrementalDeclarationPrefixParts(prefix)
	if !ok {
		return nil
	}
	record.Items = append(append([]session.Item(nil), prefix...), record.Items...)
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
	if strings.TrimSpace(instructions) != "" {
		baseSnapshot, err := session.EncodeBaseInstructionsSnapshot(windowNumber, session.BaseInstructionsHash(instructions))
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
