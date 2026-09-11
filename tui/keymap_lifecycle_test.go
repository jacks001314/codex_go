package tui

import (
	"strings"
	"testing"

	agentsoverview "codex_go/tui/agents_overview"
)

// TestAgentsArchiveDeleteDefaultsLikeRust covers #44433: archive defaults to
// ctrl-e and delete to the Delete key.
func TestAgentsArchiveDeleteDefaultsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   string
		label  string
	}{
		{action: agentsoverview.ShortcutHintArchive, want: "ctrl-e", label: "Archive"},
		{action: agentsoverview.ShortcutHintDelete, want: "delete", label: "Delete"},
	} {
		bindings, source, custom := ResolvedKeymapBindings(nil, "agents", tc.action)
		if custom || source != "default" || strings.Join(bindings, ",") != tc.want {
			t.Fatalf("agents.%s = %#v source=%q custom=%v, want default %q", tc.action, bindings, source, custom, tc.want)
		}
		if descriptor, ok := FindKeymapAction("agents", tc.action); !ok || descriptor.Label != tc.label {
			t.Fatalf("agents.%s descriptor = %#v ok=%v", tc.action, descriptor, ok)
		}
	}
}

// TestAgentsArchiveDeleteDefaultsYieldToExistingBindingsLikeRust covers the
// Rust yield rule for the newly added defaults.
func TestAgentsArchiveDeleteDefaultsYieldToExistingBindingsLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		context      string
		action       string
		binding      string
		targetAction string
	}{
		{name: "agents ctrl-e", context: "agents", action: "search", binding: "ctrl-e", targetAction: agentsoverview.ShortcutHintArchive},
		{name: "list ctrl-e", context: "list", action: "jump_top", binding: "ctrl-e", targetAction: agentsoverview.ShortcutHintArchive},
		{name: "global ctrl-e", context: "global", action: "open_agents", binding: "ctrl-e", targetAction: agentsoverview.ShortcutHintArchive},
		{name: "agents delete", context: "agents", action: "rename", binding: "delete", targetAction: agentsoverview.ShortcutHintDelete},
		{name: "list delete", context: "list", action: "delete_selected", binding: "delete", targetAction: agentsoverview.ShortcutHintDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := NewKeymapConfig()
			context := tc.context
			action := tc.action
			if context == "list" && action == "delete_selected" {
				action = "jump_top"
			}
			if err := config.Set(context, action, []string{tc.binding}); err != nil {
				t.Fatalf("Set %s.%s = %v", context, action, err)
			}
			if bindings, _, custom := ResolvedKeymapBindings(config, "agents", tc.targetAction); custom || len(bindings) != 0 {
				t.Fatalf("agents.%s = %#v custom=%v, want cleared default", tc.targetAction, bindings, custom)
			}
			if err := config.Validate(); err != nil {
				t.Fatalf("Validate = %v", err)
			}
		})
	}

	// An explicit binding wins over the yield rule.
	explicit := NewKeymapConfig()
	if err := explicit.Set("agents", "search", []string{"ctrl-e"}); err != nil {
		t.Fatalf("Set agents.search = %v", err)
	}
	if err := explicit.Set("agents", "archive", []string{"f7"}); err != nil {
		t.Fatalf("Set agents.archive = %v", err)
	}
	if bindings, source, custom := ResolvedKeymapBindings(explicit, "agents", agentsoverview.ShortcutHintArchive); !custom || source != "custom" || strings.Join(bindings, ",") != "f7" {
		t.Fatalf("explicit agents.archive = %#v source=%q custom=%v, want custom f7", bindings, source, custom)
	}
}
