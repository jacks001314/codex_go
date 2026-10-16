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
//   - rustOnlyKeymapActions: upstream actions Go does not register. Either no Go
//     behavior exists yet, or the behavior exists under a Go-specific action name
//     whose consumers live in tui/tea (outside this lane's write face). Adding a
//     name with no consumer would create a config target that never fires, so the
//     entry stays absent until behavior and consumer land together (T-b12 §T12).
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
const keymapCatalogEntryCount = 151

// rustOnlyKeymapActions: Rust actions this Go tree does not register.
var rustOnlyKeymapActions = map[string]string{
	"agents.fork":                   "d42056091a (#49517) actions.rs:275 - Go's agents dashboard has no fork action (it carries Go-only agents.toggle_pin in that slot)",
	"chat.open_presets":             "111362a6a0 (#54381) actions.rs:139 - blocked_precondition: /presets + quick dials is #54380/#54381, not implemented in Go",
	"global.find_transcript":        "df7f717c85 (#46734) actions.rs:116 - Go has no transcript search; tui/tooltips.txt:43 references it and the tip is dropped by tooltip_render.go",
	"global.focus_activity":         "df7f717c85 (#46734) actions.rs:119 - Go has no transcript activity-group focus",
	"pager.find":                    "de6ae71ad4 (#46749) actions.rs:256 - Go's transcript pager has no find bar",
	"vim_normal.enter_replace_mode": "5a0419edb5 (#42194) actions.rs:175 - behavior exists as Go-only vim_normal.replace_mode (consumer tui/tea/vim_mode.go:346)",
	"vim_normal.find_forward":       "98ee29c7cd (#40785) actions.rs:185 - behavior exists as Go-only vim_normal.find_char_forward (consumer tui/tea/vim_mode.go:385)",
	"vim_normal.find_backward":      "98ee29c7cd (#40785) actions.rs:186 - behavior exists as Go-only vim_normal.find_char_backward",
	"vim_normal.till_forward":       "98ee29c7cd (#40785) actions.rs:187 - behavior exists as Go-only vim_normal.till_char_forward",
	"vim_normal.till_backward":      "98ee29c7cd (#40785) actions.rs:188 - behavior exists as Go-only vim_normal.till_char_backward",
	"vim_search.forward":            "63d213884d (#41586) actions.rs:207 - behavior exists as Go-only vim_normal.search_forward (consumer tui/tea/vim_mode.go:369)",
	"vim_search.backward":           "63d213884d (#41586) actions.rs:208 - behavior exists as Go-only vim_normal.search_backward",
	"vim_search.next":               "63d213884d (#41586) actions.rs:209 - behavior exists as Go-only vim_normal.search_next",
	"vim_search.previous":           "63d213884d (#41586) actions.rs:210 - behavior exists as Go-only vim_normal.search_prev",
}

// goOnlyKeymapActions are catalog entries upstream does not declare. Keep them:
// they back Go-side behavior whose consumers already ask for these names.
var goOnlyKeymapActions = map[string]string{
	"agents.toggle_pin":             "Go-only agents dashboard action (tui/tea/agents_overview.go:446)",
	"global.open_warnings":          "Go-only warnings viewer shortcut (f2)",
	"vim_normal.replace_mode":       "Go name for upstream vim_normal.enter_replace_mode",
	"vim_normal.find_char_forward":  "Go name for upstream vim_normal.find_forward",
	"vim_normal.find_char_backward": "Go name for upstream vim_normal.find_backward",
	"vim_normal.till_char_forward":  "Go name for upstream vim_normal.till_forward",
	"vim_normal.till_char_backward": "Go name for upstream vim_normal.till_backward",
	"vim_normal.search_forward":     "Go name for upstream vim_search.forward",
	"vim_normal.search_backward":    "Go name for upstream vim_search.backward",
	"vim_normal.search_next":        "Go name for upstream vim_search.next",
	"vim_normal.search_prev":        "Go name for upstream vim_search.previous",
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
