package tui

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestKeymapActionCatalogIncludesRustActions(t *testing.T) {
	actions := KeymapActions(KeymapActionFilter{})
	want := []string{
		"global/open_external_editor",
		"composer/submit",
		"editor/insert_newline",
		"vim_text_object/double_quote",
		"approval/approve_for_prefix",
		"pager/close_transcript",
		"list/page_down",
		"chat/decrease_reasoning_effort",
		"vim_operator/select_inner_text_object",
	}
	seen := make(map[string]bool, len(actions))
	for _, action := range actions {
		key := action.Context + "/" + action.Action
		seen[key] = true
		if action.Context == "global" && action.Action == "toggle_fast_mode" {
			t.Fatal("toggle_fast_mode should be hidden when fast mode is disabled")
		}
	}
	for _, key := range want {
		if !seen[key] {
			t.Fatalf("KeymapActions missing %s", key)
		}
	}

	actions = KeymapActions(KeymapActionFilter{FastModeEnabled: true})
	for _, action := range actions {
		if action.Context == "global" && action.Action == "toggle_fast_mode" {
			return
		}
	}
	t.Fatal("toggle_fast_mode should be visible when fast mode is enabled")
}

func TestRenderKeymapCatalog(t *testing.T) {
	rendered := RenderKeymapCatalog(KeymapActionFilter{})
	for _, want := range []string{
		"Codex TUI keymap:",
		"  Global",
		"Open External Editor | ctrl-g",
		"Interrupt Turn | esc",
		"Insert Newline | ctrl-j, ctrl-m, enter",
		"Approve For Session | a",
		"Vim text object",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("RenderKeymapCatalog missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Toggle Fast Mode") {
		t.Fatalf("RenderKeymapCatalog should hide fast mode by default:\n%s", rendered)
	}
}

func TestKeymapActionLabel(t *testing.T) {
	if got := KeymapActionLabel("approve_for_prefix"); got != "Approve For Prefix" {
		t.Fatalf("KeymapActionLabel = %q, want Approve For Prefix", got)
	}
}

func TestNormalizeKeybindingSpecRustAliases(t *testing.T) {
	tests := map[string]string{
		" Control-Option-PageUp ": "ctrl-alt-page-up",
		"escape":                  "esc",
		"return":                  "enter",
		"spacebar":                "space",
		"F24":                     "f24",
		"minus":                   "minus",
	}
	for input, want := range tests {
		got, err := NormalizeKeybindingSpec(input)
		if err != nil {
			t.Fatalf("NormalizeKeybindingSpec(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeKeybindingSpec(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := NormalizeKeybindingSpec("f25"); err == nil {
		t.Fatal("NormalizeKeybindingSpec(f25) returned nil error")
	}
}

func TestKeymapConfigFromConfigValues(t *testing.T) {
	var values map[string]any
	input := `
[tui.keymap.global]
open_external_editor = "Control-E"

[tui.keymap.composer]
submit = ["ctrl-shift-s", "alt-enter"]
queue = []
`
	if err := toml.Unmarshal([]byte(input), &values); err != nil {
		t.Fatalf("toml.Unmarshal error = %v", err)
	}
	config, err := KeymapConfigFromConfigValues(values)
	if err != nil {
		t.Fatalf("KeymapConfigFromConfigValues error = %v", err)
	}
	if bindings, ok := config.Binding("global", "open_external_editor"); !ok || strings.Join(bindings, ",") != "ctrl-e" {
		t.Fatalf("open_external_editor bindings = %#v ok=%v", bindings, ok)
	}
	if bindings, ok := config.Binding("composer", "submit"); !ok || strings.Join(bindings, ",") != "ctrl-shift-s,alt-enter" {
		t.Fatalf("composer.submit bindings = %#v ok=%v", bindings, ok)
	}
	if bindings, ok := config.Binding("composer", "queue"); !ok || len(bindings) != 0 {
		t.Fatalf("composer.queue bindings = %#v ok=%v, want explicit unbind", bindings, ok)
	}
}

func TestKeymapConfigRejectsConflictWithEffectiveDefaultBinding(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("composer", "submit", []string{"ctrl-s"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "composer.history_search_next") {
		t.Fatalf("Validate conflict error = %v", err)
	}
}

func TestKeymapConfigRejectsUnknownAndMisplacedActions(t *testing.T) {
	for _, input := range []string{
		"[tui.keymap]\nopen_transcript = \"ctrl-s\"\n",
		"[tui.keymap.global]\nopen_transcrip = \"ctrl-s\"\n",
		"[tui.keymap.vim_text_object]\ndouble_quotes = \"shift-quote\"\n",
	} {
		var values map[string]any
		if err := toml.Unmarshal([]byte(input), &values); err != nil {
			t.Fatalf("toml.Unmarshal error = %v", err)
		}
		if _, err := KeymapConfigFromConfigValues(values); err == nil {
			t.Fatalf("KeymapConfigFromConfigValues(%q) returned nil error", input)
		}
	}
}

func TestHandleKeymapCommandSetUnbindUnset(t *testing.T) {
	config := NewKeymapConfig()
	result, err := HandleKeymapCommand("set global.open_external_editor control-e", config, nil)
	if err != nil {
		t.Fatalf("HandleKeymapCommand(set) error = %v", err)
	}
	if !strings.Contains(result.Text, "ctrl-e") {
		t.Fatalf("set result = %q, missing ctrl-e", result.Text)
	}
	if !KeymapActionHasBinding(result.Config, "global", "open_external_editor", "ctrl-e") {
		t.Fatalf("custom binding not active: %#v", result.Config)
	}

	result, err = HandleKeymapCommand("unbind global.open_external_editor", result.Config, nil)
	if err != nil {
		t.Fatalf("HandleKeymapCommand(unbind) error = %v", err)
	}
	if bindings, _, custom := ResolvedKeymapBindings(result.Config, "global", "open_external_editor"); !custom || len(bindings) != 0 {
		t.Fatalf("unbind resolved bindings = %#v custom=%v", bindings, custom)
	}

	result, err = HandleKeymapCommand("unset global.open_external_editor", result.Config, nil)
	if err != nil {
		t.Fatalf("HandleKeymapCommand(unset) error = %v", err)
	}
	if bindings, _, custom := ResolvedKeymapBindings(result.Config, "global", "open_external_editor"); custom || strings.Join(bindings, ",") != "ctrl-g" {
		t.Fatalf("unset resolved bindings = %#v custom=%v", bindings, custom)
	}
}

func TestHandleKeymapCommandSupportsGlobalComposerFallback(t *testing.T) {
	result, err := HandleKeymapCommand("set global.submit ctrl-s", NewKeymapConfig(), nil)
	if err != nil {
		t.Fatalf("HandleKeymapCommand(global submit) error = %v", err)
	}
	if !strings.Contains(result.Text, "global.submit") || !strings.Contains(result.Text, "ctrl-s") {
		t.Fatalf("global submit result = %q", result.Text)
	}
	if !KeymapActionHasBinding(result.Config, "composer", "submit", "ctrl-s") {
		t.Fatalf("global submit fallback is not active")
	}
}
