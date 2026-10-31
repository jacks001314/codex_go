package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/keymapschema"
)

// Rust parity: codex-rs/tui/src/keymap_setup/actions.rs at 4eb4a65407
// (`KEYMAP_ACTIONS`: 154 entries across 12 contexts). Go's keymapActionCatalog
// in keymap.go is the TUI's single inventory (#54151): the `/keymap` descriptors
// and the root-config target lookup in keymap_config.go both derive from it.
//
// The two inventories still differ, and this file pins the difference so it can
// only move on purpose:
//
//   - rustOnlyKeymapActions: upstream actions Go does not register and whose
//     behavior is genuinely missing. Adding a name with no consumer would create
//     a config target that never fires, so the entry stays absent until behavior
//     and consumer land together (T-b12 §T12, T-b13 §T13).
//   - acceptedKeymapRenames: upstream actions whose behavior exists in Go under a
//     different action name. T-b13 accepted these as known naming differences, so
//     the Rust name must stay out of the catalog while the Go name keeps working.
//   - goOnlyKeymapActions: Go actions with no upstream counterpart. They are
//     deliberately kept (Go-side implementations), not deleted.
//
// Landing any entry means updating the matching ledger here and the pinned
// catalog size below in the same change.
//
// keymapCatalogEntryCount pins the size of Go's catalog (153 keymapAction entries
// plus the single feature-gated keymapGatedAction entry). Every landed or dropped
// action moves it together with the ledgers, so a silent add or remove cannot
// slip past this file. T-b14 added agents.fork (#49517) and global.find_transcript
// (#46734), and moved agents.search to the upstream f3// pair, taking the pinned
// size from 152 to 154, and T-b15 added global.focus_activity (#46734),
// taking it to 155. #54381 added chat.open_presets, taking it to 156.
// #57223 adds global.select_transcript as a config-only TuiKeymap field, so it
// stays out of this catalog (Rust keeps it out of KEYMAP_ACTIONS too).
const keymapCatalogEntryCount = 156

// rustOnlyKeymapActions: Rust actions this Go tree does not register and whose
// behavior is genuinely missing (still blocked). T-b14 landed agents.fork and
// global.find_transcript, so the remaining blockers are the two below.
//
// T-b12 §T12 originally listed 14; T-b13 accepted 9 as renames and T-b14 landed 3
// (pager.find, agents.fork, global.find_transcript).
var rustOnlyKeymapActions = map[string]string{}

// goOnlyKeymapActions are catalog entries upstream does not declare. Keep them:
// they back Go-side behavior whose consumers already ask for these names.
var goOnlyKeymapActions = map[string]string{
	"agents.toggle_pin":    "Go-only agents dashboard action (tui/tea/agents_overview.go:446)",
	"global.open_warnings": "Go-only warnings viewer shortcut (f2)",
}

// acceptedKeymapRenames records upstream actions that only exist in Go under a
// different action name. T-b13 accepted nine Vim renames here; the catalog now
// carries upstream's own spellings (`enter_replace_mode`, `find_forward`,
// `till_backward`, and the separate `vim_search` context), so the ledger is
// empty and keymapschema.ActionAliases keeps the old Go names working instead.
//
// Value format: "<go context>.<go action>|<file:line> · <sha> (#PR) <reason>".
var acceptedKeymapRenames = map[string]string{}

// composerFallbackKeymapTargets are the three composer actions the loader also
// accepts under the `global` context (Rust's composer -> global submit/queue
// fallback, mirrored by FindKeymapAction).
var composerFallbackKeymapTargets = map[string]string{
	"global.submit":           "composer.submit",
	"global.queue":            "composer.queue",
	"global.toggle_shortcuts": "composer.toggle_shortcuts",
}

func splitKeymapLedgerKey(t *testing.T, key string) (string, string) {
	t.Helper()
	context, action, ok := strings.Cut(key, ".")
	if !ok || context == "" || action == "" {
		t.Fatalf("ledger key %q must be CONTEXT.ACTION", key)
	}
	return context, action
}

// splitAcceptedRename parses the GO_KEY|provenance value of acceptedKeymapRenames.
func splitAcceptedRename(t *testing.T, rename string) (string, string) {
	t.Helper()
	goKey, _, ok := strings.Cut(rename, "|")
	if !ok {
		t.Fatalf("accepted rename %q must be GO_KEY|provenance", rename)
	}
	return splitKeymapLedgerKey(t, goKey)
}

// TestKeymapCatalogIsSingleSourceForConfigTargets pins the #54151 structure: one
// catalog drives both the descriptor surface and the root-config target lookup,
// in both directions. Dropping the catalog from either derivation fails here.
func TestKeymapCatalogIsSingleSourceForConfigTargets(t *testing.T) {
	seen := map[string]bool{}
	// Config-only TuiKeymap fields are accepted targets without a setup entry
	// (Rust #57223 keeps `global.select_transcript` out of KEYMAP_ACTIONS).
	configOnly := map[string]bool{}
	for _, descriptor := range keymapConfigOnlyActions {
		configOnly[descriptor.Context+"."+descriptor.Action] = true
	}
	contexts := knownKeymapConfigContexts()
	for _, descriptor := range keymapActionCatalog {
		key := descriptor.Context + "." + descriptor.Action
		if seen[key] {
			t.Fatalf("duplicate catalog entry %s", key)
		}
		seen[key] = true
		if _, ok := contexts[descriptor.Context]; !ok {
			t.Fatalf("context %q of %s is not a known config context", descriptor.Context, key)
		}
		if _, ok := knownKeymapConfigActions(descriptor.Context)[descriptor.Action]; !ok {
			t.Fatalf("%s is not a known config action", key)
		}
		found, ok := FindKeymapAction(descriptor.Context, descriptor.Action)
		if !ok || found.Label != descriptor.Label {
			t.Fatalf("FindKeymapAction(%q, %q) = %#v, %v; want label %q", descriptor.Context, descriptor.Action, found, ok, descriptor.Label)
		}
	}
	for _, context := range sortedKeymapContexts() {
		for action := range knownKeymapConfigActions(context) {
			key := context + "." + action
			if seen[key] || configOnly[key] {
				continue
			}
			if fallback, ok := composerFallbackKeymapTargets[key]; ok {
				if _, known := FindKeymapAction(fallback[:strings.Index(fallback, ".")], fallback[strings.Index(fallback, ".")+1:]); !known {
					t.Fatalf("composer fallback target %s has no catalog entry", fallback)
				}
				continue
			}
			t.Fatalf("config target %s has no catalog entry", key)
		}
	}
}

// TestKeymapCatalogRustInventoryLedger pins both drift directions against
// upstream actions.rs at 4eb4a65407. A registered Rust-only action, a vanished
// Go-only action, or a config target that /keymap cannot act on all fail here.
func TestKeymapCatalogRustInventoryLedger(t *testing.T) {
	for key, provenance := range rustOnlyKeymapActions {
		context, action := splitKeymapLedgerKey(t, key)
		if _, ok := FindKeymapAction(context, action); ok {
			t.Fatalf("%s is registered in the catalog but the ledger still lists it as Rust-only (%s); land the consumer and delete the ledger row", key, provenance)
		}
		if err := NewKeymapConfig().Set(context, action, []string{"ctrl-u"}); err == nil {
			t.Fatalf("%s is accepted as a config target although the catalog has no such action (%s)", key, provenance)
		}
	}
	for key, rename := range acceptedKeymapRenames {
		context, action := splitKeymapLedgerKey(t, key)
		if _, ok := FindKeymapAction(context, action); ok {
			t.Fatalf("%s is registered in the catalog although it is accepted as a rename (%s); land the consumer or drop the accepted row", key, rename)
		}
		goContext, goAction := splitAcceptedRename(t, rename)
		if _, ok := FindKeymapAction(goContext, goAction); !ok {
			t.Fatalf("Go name %s.%s of accepted rename %s is missing from the catalog", goContext, goAction, key)
		}
		// Rust #54766: the upstream spelling stays a valid config target and
		// binds the Go action that implements the behavior, so a config
		// authored against upstream keeps working here.
		if alias, ok := keymapschema.ActionAliases[key]; !ok || alias != goContext+"."+goAction {
			t.Fatalf("keymapschema.ActionAliases[%q] = %q, %v; want %q", key, alias, ok, goContext+"."+goAction)
		}
		config := NewKeymapConfig()
		if err := config.Set(context, action, []string{"ctrl-u"}); err != nil {
			t.Fatalf("%s must stay a valid config target through keymapschema.ActionAliases: %v", key, err)
		}
		bindings, source, ok := ResolvedKeymapBindings(config, goContext, goAction)
		if !ok || source != "custom" || len(bindings) != 1 || bindings[0] != "ctrl-u" {
			t.Fatalf("%s resolved to %v via %q (%v); want the Go target %s.%s bound to ctrl-u", key, bindings, source, ok, goContext, goAction)
		}
	}
	for key, note := range goOnlyKeymapActions {
		context, action := splitKeymapLedgerKey(t, key)
		if _, ok := FindKeymapAction(context, action); !ok {
			t.Fatalf("Go-only action %s disappeared from the catalog (%s)", key, note)
		}
	}
	if len(keymapActionCatalog) != keymapCatalogEntryCount {
		t.Fatalf("keymapActionCatalog has %d entries; want %d (land or drop an action together with keymapCatalogEntryCount and the ledgers)", len(keymapActionCatalog), keymapCatalogEntryCount)
	}
}

// rustKeymapActionsSource locates and reads the upstream
// codex-rs/tui/src/keymap_setup/actions.rs that declares `define_keymap_actions!`.
// The search order mirrors parity.rustSnapshotRoot: CODEX_RUST_ROOT first, then
// the sibling checkout the lanes keep next to this worktree. A tree that cannot
// be found skips the upstream comparison exactly like the parity tests do, so
// `go test ./tui/` stays runnable without the Rust checkout; setting
// CODEX_RUST_ROOT to a wrong path fails loudly instead of skipping.
func rustKeymapActionsSource(t *testing.T) string {
	t.Helper()
	env := strings.TrimSpace(os.Getenv("CODEX_RUST_ROOT"))
	candidates := []string{}
	if env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "git", "codex", "codex-rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "codex-main", "codex-rs"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		for _, rel := range [][]string{
			{"tui", "src", "keymap_setup", "actions.rs"},
			{"codex-rs", "tui", "src", "keymap_setup", "actions.rs"},
		} {
			path := filepath.Join(append([]string{abs}, rel...)...)
			if data, err := os.ReadFile(path); err == nil {
				// Both known lane checkouts (the sibling git/codex tree and the
				// C:\rw\codex-rs appserver pin) export the same 154-action
				// inventory, so either resolution compares the same source.
				t.Logf("comparing keymapActionCatalog against %s", path)
				return string(data)
			}
		}
	}
	if env != "" {
		t.Fatalf("CODEX_RUST_ROOT=%q does not contain tui/src/keymap_setup/actions.rs", env)
	}
	t.Log("Rust keymap actions.rs not found; set CODEX_RUST_ROOT to compare the catalog against upstream")
	t.Skip("Rust keymap actions.rs not found")
	return ""
}

// parseRustKeymapActions extracts the CONTEXT.ACTION keys declared by
// `define_keymap_actions!` in actions.rs. It reads the real upstream file, so the
// guard below compares Go against the source of truth instead of against a second
// hand-maintained list.
func parseRustKeymapActions(t *testing.T, source string) []string {
	t.Helper()
	lines := strings.Split(source, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "define_keymap_actions! {") ||
			strings.HasPrefix(strings.TrimSpace(line), "define_keymap_actions!{") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("actions.rs does not declare define_keymap_actions!")
	}
	context := ""
	keys := []string{}
	for _, raw := range lines[start+1:] {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		// Only the unindented brace closes define_keymap_actions!; each context
		// block closes with an indented one.
		if strings.TrimRight(raw, "\r") == "}" {
			break
		}
		if strings.HasSuffix(line, "{") {
			// `global "Global" {`
			fields := strings.Fields(line)
			context = strings.TrimSuffix(fields[0], "{")
			continue
		}
		arrow := strings.Index(line, "=>")
		if arrow < 0 || context == "" {
			continue
		}
		ident := strings.TrimSpace(line[:arrow])
		if space := strings.IndexAny(ident, " \t"); space >= 0 {
			// `toggle_fast_mode [FastMode]` carries a required feature.
			ident = strings.TrimSpace(ident[:space])
		}
		if ident == "" {
			continue
		}
		keys = append(keys, context+"."+ident)
	}
	if len(keys) == 0 {
		t.Fatalf("parsed no keymap actions from actions.rs")
	}
	return keys
}

// TestKeymapCatalogMatchesRustActionsLikeRust closes the coverage hole T-b16/V-b17
// found: the ledger checks used to verify membership only, so replacing a shared
// action with an equal-length name (`global.copy` -> `global.copy_zz`) kept
// len(catalog) at the pinned value and passed silently. This test compares the
// catalog key set against upstream `define_keymap_actions!`:
//
//	{catalog keys} ==
//	    (RUST_KEYS - rustOnlyKeymapActions - acceptedKeymapRenames Rust names)
//	    + {acceptedKeymapRenames Go names} + goOnlyKeymapActions
//
// so an action may only exist upstream-and-here, upstream-and-ledgered, or
// Go-only-and-listed.
func TestKeymapCatalogMatchesRustActionsLikeRust(t *testing.T) {
	rustKeys := parseRustKeymapActions(t, rustKeymapActionsSource(t))
	rust := make(map[string]bool, len(rustKeys))
	for _, key := range rustKeys {
		if rust[key] {
			t.Fatalf("upstream actions.rs declares %s twice", key)
		}
		rust[key] = true
	}
	catalog := map[string]bool{}
	for _, descriptor := range keymapActionCatalog {
		catalog[descriptor.Context+"."+descriptor.Action] = true
	}
	goOnly := map[string]bool{}
	for key := range goOnlyKeymapActions {
		goOnly[key] = true
	}
	for key, rename := range acceptedKeymapRenames {
		if !rust[key] {
			t.Fatalf("accepted rename %s is not an upstream action", key)
		}
		goContext, goAction := splitAcceptedRename(t, rename)
		goOnly[goContext+"."+goAction] = true
	}
	for key := range rustOnlyKeymapActions {
		if !rust[key] {
			t.Fatalf("rustOnlyKeymapActions lists %s, which upstream does not declare", key)
		}
	}
	// Direction 1: every upstream action is catalogued, ledgered, or renamed.
	for _, key := range rustKeys {
		if catalog[key] || rustOnlyKeymapActions[key] != "" || acceptedKeymapRenames[key] != "" {
			continue
		}
		t.Fatalf("upstream action %s is neither in the catalog nor in the ledgers; land the consumer and the catalog entry, or ledger it", key)
	}
	// Direction 2: the catalog carries no name upstream does not declare.
	for key := range catalog {
		if goOnly[key] {
			continue
		}
		if !rust[key] {
			t.Fatalf("catalog action %s has no upstream counterpart in define_keymap_actions! and is not listed in goOnlyKeymapActions; an equal-length rename cannot pass here", key)
		}
	}
	// A truncated parse would silently shrink RUST_KEYS, so pin the parsed shape:
	// upstream declares 154 actions across 12 contexts (actions.rs:112-293).
	contexts := map[string]bool{}
	for _, key := range rustKeys {
		context, _, _ := strings.Cut(key, ".")
		contexts[context] = true
	}
	if len(rustKeys) != 154 || len(contexts) != 12 {
		t.Fatalf("parsed %d upstream actions across %d contexts, want 154 across 12", len(rustKeys), len(contexts))
	}
}
