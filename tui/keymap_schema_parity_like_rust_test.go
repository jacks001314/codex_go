package tui

import (
	"testing"

	"codex_go/keymapschema"
)

// The strict-config validator reads keymapschema, while the TUI reads
// keymapActionCatalog. Rust #54766 derives the strict policy from the same
// structs the runtime deserializes, so these two tables must stay identical.
func TestKeymapSchemaMatchesCatalogLikeRust(t *testing.T) {
	catalog := map[string]map[string]bool{}
	for _, action := range keymapActionCatalog {
		if catalog[action.Context] == nil {
			catalog[action.Context] = map[string]bool{}
		}
		catalog[action.Context][action.Action] = true
	}
	// Rust accepts the composer aliases in the global context too.
	for _, action := range []string{"submit", "queue", "toggle_shortcuts"} {
		if catalog["global"] == nil {
			catalog["global"] = map[string]bool{}
		}
		catalog["global"][action] = true
	}
	if len(catalog) != len(keymapschema.ContextActions) {
		t.Fatalf("schema contexts = %d, catalog contexts = %d", len(keymapschema.ContextActions), len(catalog))
	}
	for context, actions := range catalog {
		schema := keymapschema.ContextActions[context]
		if schema == nil {
			t.Fatalf("context %q missing from keymapschema", context)
		}
		if len(actions) != len(schema) {
			t.Fatalf("context %q: schema actions = %d, catalog actions = %d", context, len(schema), len(actions))
		}
		for action := range actions {
			if !keymapschema.KnownAction(context, action) {
				t.Fatalf("action %s.%s missing from keymapschema", context, action)
			}
		}
	}
}
