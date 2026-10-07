package model

import (
	"encoding/json"
	"strings"
)

// A responses-lite context window advertises its tool catalog through a
// harness-authored `additional_tools` item followed by a developer message with
// the window's base instructions. Rust #51480
// (`Session::current_window_uses_incremental_tools`) decides per context window
// whether those declarations are recorded in the window's history and replayed
// unchanged (incremental) or rebuilt for every request (legacy), so changing the
// incremental-tools setting on resume never moves declarations inside a live
// window.
type ToolDeclarationMode string

const (
	// ToolDeclarationLegacy rebuilds the declaration prefix for every request of
	// the window, the pre-#51480 responses-lite shape.
	ToolDeclarationLegacy ToolDeclarationMode = "legacy"
	// ToolDeclarationIncremental records the declaration once, at window start,
	// and replays the recorded items for the rest of the window.
	ToolDeclarationIncremental ToolDeclarationMode = "incremental"
)

// Record metadata keys the appserver stores the frozen decision under. They live
// in `session.Record.Metadata.Extra`, the Go equivalent of the rollout metadata
// Rust stamps on a context window.
const (
	ToolDeclarationWindowNumberKey = "tool_declaration_window_number"
	ToolDeclarationModeKey         = "tool_declaration_mode"
	ToolDeclarationItemsKey        = "tool_declarations"
)

// ResolveToolDeclarationMode mirrors Rust `current_window_uses_incremental_tools`
// (#51480): a non-lite model never adopts incremental tools; a new context window
// (empty history) applies the current setting; an existing window keeps whatever
// its history says, so a resumed window with recorded declarations stays
// incremental and one without stays legacy.
func ResolveToolDeclarationMode(history []any, useResponsesLite bool, incrementalToolsEnabled bool) ToolDeclarationMode {
	if !useResponsesLite {
		return ToolDeclarationLegacy
	}
	if len(history) == 0 {
		if incrementalToolsEnabled {
			return ToolDeclarationIncremental
		}
		return ToolDeclarationLegacy
	}
	if WindowHasToolDeclarations(history) {
		return ToolDeclarationIncremental
	}
	return ToolDeclarationLegacy
}

// ResolveToolDeclarationWindow reports the declaration decision for the thread's
// current context window and whether the persisted decision must be updated.
//
// stored carries the window already frozen on the record (may be nil). A window
// whose number still matches keeps its frozen mode and declarations, so a live
// window never rebuilds or moves them. A window replacement - a compaction or a
// context reset advances the window number, and a brand new thread has no frozen
// window at all - re-derives the mode from the current setting. An existing
// window that predates the frozen decision (an older thread) keeps the legacy
// shape, mirroring Rust's legacy-resume case.
func ResolveToolDeclarationWindow(stored map[string]any, history []any, currentWindowNumber uint64, useResponsesLite bool, incrementalToolsEnabled bool) (ToolDeclarationMode, []any, bool) {
	if !useResponsesLite {
		return ToolDeclarationLegacy, nil, false
	}
	windowNumber, windowKnown := storedToolDeclarationWindowNumber(stored)
	if windowKnown && windowNumber == currentWindowNumber {
		// The live window keeps whatever it recorded, even when the incremental
		// tools setting changed on resume (Rust #51480: disabling incremental
		// tools preserves the updates of the existing window).
		if toolDeclarationModeFromStored(stored) == ToolDeclarationIncremental {
			return ToolDeclarationIncremental, toolDeclarationItemsFromStored(stored), false
		}
		return ToolDeclarationLegacy, nil, false
	}
	if !incrementalToolsEnabled && !windowKnown {
		// Nothing was ever recorded for this thread and incremental tools are
		// off, so the window keeps rebuilding its prefix per request and the
		// record is left untouched (the pre-#51480 responses-lite shape).
		return ToolDeclarationLegacy, nil, false
	}
	// A window replacement - a compaction or a context reset advances the window
	// number - or a brand new thread adopts the current setting. An existing
	// window recorded before the decision existed keeps the legacy shape
	// (Rust legacy resume).
	newWindow := windowKnown || len(history) == 0
	mode := ToolDeclarationLegacy
	if newWindow && incrementalToolsEnabled {
		mode = ToolDeclarationIncremental
	}
	return mode, nil, true
}

// ResponsesLiteDeclarationItems builds the harness-authored declaration prefix a
// responses-lite window records once: the `additional_tools` catalog and, when
// the window carries instructions, the base-instruction developer message. The
// items are normalized through JSON so the in-memory and the reloaded
// representation serialize byte-for-byte the same, which keeps the server-side
// prefix cache warm across turns.
func ResponsesLiteDeclarationItems(tools []any, instructions string, threadID string) []any {
	items := responsesLiteInputItems(nil, tools, instructions, threadID)
	out := make([]any, 0, len(items))
	for _, item := range items {
		normalized, ok := normalizeResponsesInputValue(item)
		if !ok {
			normalized = item
		}
		out = append(out, normalized)
	}
	return out
}

// ResponsesLiteItemID derives the deterministic id of a harness-authored
// responses-lite prefix item (Rust client.rs #40962): a UUIDv5 keyed by the
// thread id plus the serialized payload, so an unchanged prefix item keeps its
// identity across retries and resumed sessions. prefix is the item kind tag
// ("at" for `additional_tools`, "msg" for a developer message).
func ResponsesLiteItemID(prefix string, payload []byte, threadID string) string {
	return responsesLiteItemID(prefix, payload, threadID)
}

// ToolDeclarationModeFromStored reads the mode a record froze for its window.
func ToolDeclarationModeFromStored(stored map[string]any) ToolDeclarationMode {
	return toolDeclarationModeFromStored(stored)
}

// ToolDeclarationItemsFromStored reads the declaration items a record froze for
// its window. The value round-trips through JSON, so it may be a []any of
// map[string]any or a []map[string]any.
func ToolDeclarationItemsFromStored(stored map[string]any) []any {
	return toolDeclarationItemsFromStored(stored)
}

// ToolDeclarationWindowNumberFromStored reads the window number a record froze,
// reporting whether any decision was recorded at all.
func ToolDeclarationWindowNumberFromStored(stored map[string]any) (uint64, bool) {
	return storedToolDeclarationWindowNumber(stored)
}

func storedToolDeclarationWindowNumber(stored map[string]any) (uint64, bool) {
	if len(stored) == 0 {
		return 0, false
	}
	value, ok := stored[ToolDeclarationWindowNumberKey]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case uint64:
		return typed, true
	case int:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case int64:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case float64:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil || parsed < 0 {
			return 0, false
		}
		return uint64(parsed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		normalized, ok := normalizeResponsesInputValue(trimmed)
		if !ok {
			return 0, false
		}
		if number, ok := normalized.(float64); ok && number >= 0 {
			return uint64(number), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func toolDeclarationModeFromStored(stored map[string]any) ToolDeclarationMode {
	if len(stored) == 0 {
		return ToolDeclarationLegacy
	}
	value := stored[ToolDeclarationModeKey]
	mode, _ := value.(string)
	if strings.EqualFold(strings.TrimSpace(mode), string(ToolDeclarationIncremental)) {
		return ToolDeclarationIncremental
	}
	return ToolDeclarationLegacy
}

func toolDeclarationItemsFromStored(stored map[string]any) []any {
	if len(stored) == 0 {
		return nil
	}
	value, ok := stored[ToolDeclarationItemsKey]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for i := range typed {
			items = append(items, typed[i])
		}
		return items
	case json.RawMessage:
		normalized, ok := normalizeResponsesInputValue(typed)
		if !ok {
			return nil
		}
		items, _ := normalized.([]any)
		return items
	default:
		normalized, ok := normalizeResponsesInputValue(value)
		if !ok {
			return nil
		}
		items, _ := normalized.([]any)
		return items
	}
}
