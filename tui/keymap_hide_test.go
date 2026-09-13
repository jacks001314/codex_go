package tui

import (
	"strings"
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
)

// TestAgentsHideShortcutDefaultsLikeRust covers #44424/#45255: agents.hide
// defaults to the plain h shortcut and surfaces through the dashboard footer
// hint.
func TestAgentsHideShortcutDefaultsLikeRust(t *testing.T) {
	bindings, source, ok := ResolvedKeymapBindings(nil, "agents", "hide")
	if ok || source != "default" || strings.Join(bindings, ",") != "h" {
		t.Fatalf("agents.hide = %#v source=%q custom=%v, want default h", bindings, source, ok)
	}
	if descriptor, ok := FindKeymapAction("agents", agentsoverview.ShortcutHintHide); !ok || descriptor.Label != "Hide" {
		t.Fatalf("agents hide action descriptor = %#v ok=%v", descriptor, ok)
	}
}

// TestAgentsHideDefaultYieldsToExistingBindingLikeRust covers the Rust yield
// rule: the new default must not shadow an existing h binding on the agents or
// list surface.
func TestAgentsHideDefaultYieldsToExistingBindingLikeRust(t *testing.T) {
	agentsConfig := NewKeymapConfig()
	if err := agentsConfig.Set("agents", "search", []string{"h"}); err != nil {
		t.Fatalf("Set agents.search = %v", err)
	}
	if bindings, _, ok := ResolvedKeymapBindings(agentsConfig, "agents", "hide"); ok || len(bindings) != 0 {
		t.Fatalf("agents.hide = %#v ok=%v, want cleared default", bindings, ok)
	}
	if err := agentsConfig.Validate(); err != nil {
		t.Fatalf("Validate with h on agents.search = %v", err)
	}

	listConfig := NewKeymapConfig()
	if err := listConfig.Set("list", "jump_top", []string{"h"}); err != nil {
		t.Fatalf("Set list.jump_top = %v", err)
	}
	if bindings, _, ok := ResolvedKeymapBindings(listConfig, "agents", "hide"); ok || len(bindings) != 0 {
		t.Fatalf("agents.hide = %#v ok=%v, want cleared default for list h", bindings, ok)
	}

	// An explicit agents.hide binding wins over the yield rule.
	explicit := NewKeymapConfig()
	if err := explicit.Set("agents", "search", []string{"h"}); err != nil {
		t.Fatalf("Set agents.search = %v", err)
	}
	if err := explicit.Set("agents", "hide", []string{"f7"}); err != nil {
		t.Fatalf("Set agents.hide = %v", err)
	}
	if bindings, source, ok := ResolvedKeymapBindings(explicit, "agents", "hide"); !ok || source != "custom" || strings.Join(bindings, ",") != "f7" {
		t.Fatalf("explicit agents.hide = %#v source=%q ok=%v, want custom f7", bindings, source, ok)
	}
}
