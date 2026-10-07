package model

import (
	"encoding/json"
	"testing"
)

// TestResolveToolDeclarationModeLikeRust mirrors Rust #51480
// `Session::current_window_uses_incremental_tools`:
//
//	if !use_responses_lite { false }
//	if history.is_empty() { incremental_tools_enabled() }
//	history.has_tool_declarations()
func TestResolveToolDeclarationModeLikeRust(t *testing.T) {
	declared := []any{map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}}}
	plain := []any{map[string]any{"type": "message", "role": "user", "content": "hello"}}

	cases := []struct {
		name        string
		history     []any
		lite        bool
		incremental bool
		want        ToolDeclarationMode
	}{
		{name: "responses api never uses incremental tools", history: plain, lite: false, incremental: true, want: ToolDeclarationLegacy},
		{name: "new window with incremental tools", history: nil, lite: true, incremental: true, want: ToolDeclarationIncremental},
		{name: "new window without incremental tools", history: nil, lite: true, incremental: false, want: ToolDeclarationLegacy},
		{name: "existing window keeps recorded declarations", history: declared, lite: true, incremental: false, want: ToolDeclarationIncremental},
		{name: "existing window without declarations stays legacy", history: plain, lite: true, incremental: true, want: ToolDeclarationLegacy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveToolDeclarationMode(tc.history, tc.lite, tc.incremental); got != tc.want {
				t.Fatalf("ResolveToolDeclarationMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveToolDeclarationWindowLikeRust covers the request-history scenarios
// of Rust `core/tests/suite/scenarios_incremental_tools_resume.rs` at the
// decision level: legacy resume, migration after remote compaction or a context
// reset, disabling incremental tools, and tool updates after restart.
func TestResolveToolDeclarationWindowLikeRust(t *testing.T) {
	declared := []any{map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}}}
	plain := []any{map[string]any{"type": "message", "role": "user", "content": "hello"}}

	t.Run("legacy_resume keeps the existing window legacy", func(t *testing.T) {
		// An existing window that predates the frozen decision is not a window
		// replacement, so it keeps the legacy shape (Rust legacy resume).
		mode, items, changed := ResolveToolDeclarationWindow(nil, plain, 0, true, true)
		if mode != ToolDeclarationLegacy || items != nil || !changed {
			t.Fatalf("legacy resume = (%q, %v, %v)", mode, items, changed)
		}
	})

	t.Run("migration after remote compaction or window reset", func(t *testing.T) {
		// request 3 / the context reset advances the window number; the next
		// request starts a new window and adopts the current setting.
		stored := map[string]any{
			ToolDeclarationWindowNumberKey: float64(0),
			ToolDeclarationModeKey:         string(ToolDeclarationLegacy),
		}
		mode, items, changed := ResolveToolDeclarationWindow(stored, plain, 1, true, true)
		if mode != ToolDeclarationIncremental || items != nil || !changed {
			t.Fatalf("migration = (%q, %v, %v)", mode, items, changed)
		}
	})

	t.Run("disabling incremental tools leaves an unrecorded window alone", func(t *testing.T) {
		// With incremental tools disabled and nothing recorded yet the request
		// keeps rebuilding its declarations (the Go legacy shape) and the record
		// is untouched.
		mode, items, changed := ResolveToolDeclarationWindow(nil, plain, 0, true, false)
		if mode != ToolDeclarationLegacy || items != nil || changed {
			t.Fatalf("disabled = (%q, %v, %v)", mode, items, changed)
		}
	})

	t.Run("disabling incremental tools preserves the live window", func(t *testing.T) {
		// Rust `disable_incremental_tools_at_next_window`: the existing window
		// keeps the declarations it recorded, whatever the new setting says.
		recorded := []any{map[string]any{"id": "at_recorded", "type": "additional_tools", "role": "developer", "tools": []any{}}}
		stored := map[string]any{
			ToolDeclarationWindowNumberKey: float64(0),
			ToolDeclarationModeKey:         string(ToolDeclarationIncremental),
			ToolDeclarationItemsKey:        recorded,
		}
		mode, items, changed := ResolveToolDeclarationWindow(stored, declared, 0, true, false)
		if mode != ToolDeclarationIncremental || changed || len(items) != 1 {
			t.Fatalf("live window = (%q, %v, %v)", mode, items, changed)
		}
	})

	t.Run("a replacement window adopts the disabled setting", func(t *testing.T) {
		stored := map[string]any{
			ToolDeclarationWindowNumberKey: float64(0),
			ToolDeclarationModeKey:         string(ToolDeclarationIncremental),
			ToolDeclarationItemsKey:        declared,
		}
		mode, items, changed := ResolveToolDeclarationWindow(stored, plain, 1, true, false)
		if mode != ToolDeclarationLegacy || items != nil || !changed {
			t.Fatalf("disabled replacement = (%q, %v, %v)", mode, items, changed)
		}
	})

	t.Run("tool updates after restart keep the recorded window", func(t *testing.T) {
		// A restart restores the same window number, so the frozen declarations
		// are reused even though the tools changed.
		recorded := []any{map[string]any{"id": "at_recorded", "type": "additional_tools", "role": "developer", "tools": []any{}}}
		stored := map[string]any{
			ToolDeclarationWindowNumberKey: float64(2),
			ToolDeclarationModeKey:         string(ToolDeclarationIncremental),
			ToolDeclarationItemsKey:        recorded,
		}
		mode, items, changed := ResolveToolDeclarationWindow(stored, declared, 2, true, true)
		if mode != ToolDeclarationIncremental || changed {
			t.Fatalf("restart = (%q, %v)", mode, changed)
		}
		if len(items) != 1 || items[0].(map[string]any)["id"] != "at_recorded" {
			t.Fatalf("recorded declarations = %#v", items)
		}
	})

	t.Run("an already recorded window is never rebuilt", func(t *testing.T) {
		recorded := []any{map[string]any{"id": "at_recorded", "type": "additional_tools", "role": "developer", "tools": []any{}}}
		stored := map[string]any{
			ToolDeclarationWindowNumberKey: float64(0),
			ToolDeclarationModeKey:         string(ToolDeclarationIncremental),
			ToolDeclarationItemsKey:        recorded,
		}
		if _, items, changed := ResolveToolDeclarationWindow(stored, declared, 0, true, true); changed || len(items) != 1 {
			t.Fatalf("live window = (%v, %v)", items, changed)
		}
	})
}

// TestResponsesLiteDeclarationItemsLikeRust checks the harness-authored prefix a
// responses-lite window records: the `additional_tools` catalog plus the base
// instruction developer message, with deterministic IDs (Rust client.rs #40962)
// and a JSON-stable representation.
func TestResponsesLiteDeclarationItemsLikeRust(t *testing.T) {
	tools := []any{map[string]any{"type": "function", "name": "exec_command"}}
	instructions := "Use the available tools to help the user."

	first := ResponsesLiteDeclarationItems(tools, instructions, "thread-abc")
	if len(first) != 2 {
		t.Fatalf("items = %#v", first)
	}
	declaration, ok := first[0].(map[string]any)
	if !ok || declaration["type"] != "additional_tools" || declaration["role"] != "developer" {
		t.Fatalf("additional_tools = %#v", first[0])
	}
	message, ok := first[1].(map[string]any)
	if !ok || message["type"] != "message" || message["role"] != "developer" {
		t.Fatalf("base instructions = %#v", first[1])
	}

	second := ResponsesLiteDeclarationItems(tools, instructions, "thread-abc")
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("declaration items are not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}

	// A different thread changes the deterministic IDs.
	other := ResponsesLiteDeclarationItems(tools, instructions, "thread-xyz")
	otherDeclaration, _ := other[0].(map[string]any)
	if otherDeclaration["id"] == declaration["id"] {
		t.Fatalf("additional_tools id did not change across threads: %v", declaration["id"])
	}

	// Changing the catalog changes the declaration identity.
	changed := ResponsesLiteDeclarationItems([]any{map[string]any{"type": "function", "name": "other"}}, instructions, "thread-abc")
	changedDeclaration, _ := changed[0].(map[string]any)
	if changedDeclaration["id"] == declaration["id"] {
		t.Fatalf("additional_tools id did not change across catalogs: %v", declaration["id"])
	}

	// The items are JSON-round-trip stable, so a reloaded record serializes the
	// same bytes as the in-memory one.
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	var decoded []any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	reloaded, _ := json.Marshal(decoded)
	if string(reloaded) != string(encoded) {
		t.Fatalf("declaration items are not JSON stable:\n%s\n%s", encoded, reloaded)
	}
}
