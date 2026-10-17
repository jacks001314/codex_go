package tui

import (
	"strings"
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

// Every accepted upstream rename must be in the schema's alias table (the
// strict-config validator reads it) and every alias must name a real catalog
// entry, so an alias can never point at behavior that does not exist.
func TestKeymapSchemaAliasesMatchCatalogLikeRust(t *testing.T) {
	catalog := map[string]bool{}
	for _, action := range keymapActionCatalog {
		catalog[action.Context+"."+action.Action] = true
	}
	for source, target := range keymapschema.ActionAliases {
		if !catalog[target] {
			t.Fatalf("alias %s -> %s has no catalog entry", source, target)
		}
		if catalog[source] {
			t.Fatalf("alias source %s is already a catalog action", source)
		}
		context, action, ok := strings.Cut(source, ".")
		if !ok || !keymapschema.KnownContext(context) || !keymapschema.KnownAction(context, action) {
			t.Fatalf("alias %s is not accepted by the schema", source)
		}
	}
	if len(keymapschema.ActionAliases) != len(acceptedKeymapRenames) {
		t.Fatalf("schema aliases = %d, accepted renames = %d", len(keymapschema.ActionAliases), len(acceptedKeymapRenames))
	}
}
