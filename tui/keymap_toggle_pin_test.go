package tui

import (
	"strings"
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
)

// TestAgentsTogglePinDefaultLikeRust covers Rust #51500: the new
// agents.toggle_pin action defaults to the plain `p` shortcut and surfaces
// through the dashboard pin/unpin hint.
func TestAgentsTogglePinDefaultLikeRust(t *testing.T) {
	bindings, source, ok := ResolvedKeymapBindings(nil, "agents", "toggle_pin")
	if ok || source != "default" || strings.Join(bindings, ",") != "p" {
		t.Fatalf("agents.toggle_pin = %#v source=%q custom=%v, want default p", bindings, source, ok)
	}
	if descriptor, ok := FindKeymapAction("agents", agentsoverview.ShortcutHintTogglePin); !ok || descriptor.Label != "Toggle Pin" {
		t.Fatalf("agents toggle_pin descriptor = %#v ok=%v", descriptor, ok)
	}
}

// TestAgentsTogglePinDefaultYieldsToExistingBindingLikeRust covers the Rust
// yield rule: the new `p` default must not shadow an existing p binding on the
// agents, list, or global surface.
func TestAgentsTogglePinDefaultYieldsToExistingBindingLikeRust(t *testing.T) {
	agentsConfig := NewKeymapConfig()
	if err := agentsConfig.Set("agents", "search", []string{"p"}); err != nil {
		t.Fatalf("Set agents.search = %v", err)
	}
	if bindings, _, ok := ResolvedKeymapBindings(agentsConfig, "agents", "toggle_pin"); ok || len(bindings) != 0 {
		t.Fatalf("agents.toggle_pin = %#v ok=%v, want cleared default", bindings, ok)
	}
	if err := agentsConfig.Validate(); err != nil {
		t.Fatalf("Validate with p on agents.search = %v", err)
	}

	globalConfig := NewKeymapConfig()
	if err := globalConfig.Set("global", "open_warnings", []string{"p"}); err != nil {
		t.Fatalf("Set global.open_warnings = %v", err)
	}
	if bindings, _, ok := ResolvedKeymapBindings(globalConfig, "agents", "toggle_pin"); ok || len(bindings) != 0 {
		t.Fatalf("agents.toggle_pin = %#v ok=%v, want cleared default for global p", bindings, ok)
	}

	// An explicit agents.toggle_pin binding wins over the yield rule.
	explicit := NewKeymapConfig()
	if err := explicit.Set("agents", "search", []string{"p"}); err != nil {
		t.Fatalf("Set agents.search = %v", err)
	}
	if err := explicit.Set("agents", "toggle_pin", []string{"f7"}); err != nil {
		t.Fatalf("Set agents.toggle_pin = %v", err)
	}
	if bindings, source, ok := ResolvedKeymapBindings(explicit, "agents", "toggle_pin"); !ok || source != "custom" || strings.Join(bindings, ",") != "f7" {
		t.Fatalf("explicit agents.toggle_pin = %#v source=%q ok=%v, want custom f7", bindings, source, ok)
	}
}
