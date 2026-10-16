package tui

import (
	"strings"
	"testing"
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
// keymapCatalogEntryCount pins the size of Go's catalog (150 keymapAction entries
// plus the single feature-gated keymapGatedAction entry). Every landed or dropped
// action moves it together with the ledgers, so a silent add or remove cannot
// slip past this file.
const keymapCatalogEntryCount = 152

// rustOnlyKeymapActions: Rust actions this Go tree does not register.
var rustOnlyKeymapActions = map[string]string{
	"agents.fork":            "d42056091a (#49517) actions.rs:275 - Go's agents dashboard has no fork action (it carries Go-only agents.toggle_pin in that slot)",
	"chat.open_presets":      "111362a6a0 (#54381) actions.rs:139 - blocked_precondition: /presets + quick dials is #54380/#54381, not implemented in Go",
	"global.find_transcript": "df7f717c85 (#46734) actions.rs:116 - Go has no transcript search; tui/tooltips.txt:43 references it and the tip is dropped by tooltip_render.go",
	"global.focus_activity":  "df7f717c85 (#46734) actions.rs:119 - Go has no transcript activity-group focus",
}

// goOnlyKeymapActions are catalog entries upstream does not declare. Keep them:
// they back Go-side behavior whose consumers already ask for these names.
var goOnlyKeymapActions = map[string]string{
	"agents.toggle_pin":    "Go-only agents dashboard action (tui/tea/agents_overview.go:446)",
	"global.open_warnings": "Go-only warnings viewer shortcut (f2)",
}

// acceptedKeymapRenames records the upstream actions whose behavior already
// exists in Go under a different action name (and, for vim search, a different
// context). T-b13 accepted them as known naming differences: renaming would have
// to move the catalog and the tui/tea consumers together, and the lane's
// bandwidth goes to the actions whose behavior is genuinely missing. The Rust
// name therefore stays out of the catalog while the Go name must keep working.
//
// Value format: "<go context>.<go action>|<file:line> · <sha> (#PR) <reason>".
var acceptedKeymapRenames = map[string]string{
	"vim_normal.enter_replace_mode": "vim_normal.replace_mode|tui/tea/vim_mode.go:346 · 5a0419edb5 (#42194) actions.rs:175",
	"vim_normal.find_forward":       "vim_normal.find_char_forward|tui/tea/vim_mode.go:385 · 98ee29c7cd (#40785) actions.rs:185",
	"vim_normal.find_backward":      "vim_normal.find_char_backward|tui/tea/vim_mode.go:387 · 98ee29c7cd (#40785) actions.rs:186",
	"vim_normal.till_forward":       "vim_normal.till_char_forward|tui/tea/vim_mode.go:389 · 98ee29c7cd (#40785) actions.rs:187",
	"vim_normal.till_backward":      "vim_normal.till_char_backward|tui/tea/vim_mode.go:391 · 98ee29c7cd (#40785) actions.rs:188",
	"vim_search.forward":            "vim_normal.search_forward|tui/tea/vim_mode.go:264/369 · 63d213884d (#41586) actions.rs:207",
	"vim_search.backward":           "vim_normal.search_backward|tui/tea/vim_mode.go:269/379 · 63d213884d (#41586) actions.rs:208",
	"vim_search.next":               "vim_normal.search_next|tui/tea/vim_mode.go:272/381 · 63d213884d (#41586) actions.rs:209",
	"vim_search.previous":           "vim_normal.search_prev|tui/tea/vim_mode.go:277/383 · 63d213884d (#41586) actions.rs:210",
}

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
			if seen[key] {
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
		if err := NewKeymapConfig().Set(context, action, []string{"ctrl-u"}); err == nil {
			t.Fatalf("%s is accepted as a config target although only the Go name is wired (%s)", key, rename)
		}
		goContext, goAction := splitAcceptedRename(t, rename)
		if _, ok := FindKeymapAction(goContext, goAction); !ok {
			t.Fatalf("Go name %s.%s of accepted rename %s is missing from the catalog", goContext, goAction, key)
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
