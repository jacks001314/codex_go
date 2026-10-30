package tui

import "testing"

// Mirrors Rust #57876: resolving the default keymap configuration yields the
// cached runtime defaults. Go has no RuntimeKeymap cache; ResolvedKeymapBindings
// is a pure lookup, so the equivalent invariant is that an empty configuration
// resolves exactly like the built-in defaults for every catalog action.
func TestDefaultKeymapConfigResolvesToBuiltInDefaultsLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	for _, action := range KeymapConfigActions(KeymapActionFilter{}) {
		wantBindings, wantSource, wantOK := ResolvedKeymapBindings(nil, action.Context, action.Action)
		gotBindings, gotSource, gotOK := ResolvedKeymapBindings(config, action.Context, action.Action)
		if gotOK != wantOK || gotSource != wantSource || !equalStrings(gotBindings, wantBindings) {
			t.Fatalf("%s.%s = (%v, %q, %v), want (%v, %q, %v)",
				action.Context, action.Action, gotBindings, gotSource, gotOK, wantBindings, wantSource, wantOK)
		}
	}
	if len(config.bindings) != 0 {
		t.Fatalf("default config mutated: %#v", config.bindings)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
