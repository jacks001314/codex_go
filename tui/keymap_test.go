package tui

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestKeymapActionCatalogIncludesRustActions(t *testing.T) {
	actions := KeymapActions(KeymapActionFilter{})
	want := []string{
		"global/open_agents",
		"agents/resume",
		"agents/search",
		"agents/new_task",
		"agents/rename",
		"agents/stop",
		"agents/toggle_grouping",
		"agents/toggle_pin",
		"global/open_external_editor",
		"composer/submit",
		"editor/insert_newline",
		"vim_text_object/double_quote",
		"vim_normal/replace_char",
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

func TestOpenAgentsDefaultBindingUnboundLikeRust(t *testing.T) {
	// Rust #41917: the empty-composer Left shortcut replaced the configurable
	// Alt+A default, so `global.open_agents` starts unbound.
	config := NewKeymapConfig()
	bindings, source, custom := ResolvedKeymapBindings(config, "global", "open_agents")
	if custom || source != "default" || len(bindings) != 0 {
		t.Fatalf("open_agents resolved bindings = %#v source=%q custom=%v, want unbound", bindings, source, custom)
	}
	if KeymapActionHasBinding(config, "global", "open_agents", "alt-a") {
		t.Fatal("open_agents must not resolve the retired alt-a default")
	}

	// A user binding still works: only the built-in default was removed.
	if err := config.Set("global", "open_agents", []string{"alt-a"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
	bindings, source, custom = ResolvedKeymapBindings(config, "global", "open_agents")
	if !custom || strings.Join(bindings, ",") != "alt-a" {
		t.Fatalf("custom open_agents bindings = %#v source=%q custom=%v, want alt-a", bindings, source, custom)
	}
}

func TestAgentsKeymapActionsResolveDefaults(t *testing.T) {
	config := NewKeymapConfig()
	for action, want := range map[string]string{
		"resume":          "o",
		"search":          "f3,/",
		"fork":            "f",
		"new_task":        "n",
		"new_worktree":    "w",
		"rename":          "r",
		"stop":            "x",
		"archive":         "a",
		"hide":            "h",
		"toggle_grouping": "g",
		"toggle_pin":      "p",
	} {
		bindings, source, custom := ResolvedKeymapBindings(config, "agents", action)
		if custom || source != "default" || strings.Join(bindings, ",") != want {
			t.Fatalf("agents.%s resolved bindings = %#v source=%q custom=%v, want %s", action, bindings, source, custom, want)
		}
	}
}

func TestAgentsKeymapCustomBinding(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("agents", "search", []string{"ctrl-l"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate error = %v", err)
	}
	if !KeymapActionHasBinding(config, "agents", "search", "ctrl-l") {
		t.Fatal("custom agents.search binding is not active")
	}
	if KeymapActionHasBinding(config, "agents", "search", "ctrl-f") {
		t.Fatal("default agents.search binding should not remain after custom set")
	}
}

func TestAgentsKeymapConfigParsing(t *testing.T) {
	var values map[string]any
	input := `
[tui.keymap.global]
open_agents = "alt-a"

[tui.keymap.agents]
search = "ctrl-l"
stop = []
`
	if err := toml.Unmarshal([]byte(input), &values); err != nil {
		t.Fatalf("toml.Unmarshal error = %v", err)
	}
	config, err := KeymapConfigFromConfigValues(values)
	if err != nil {
		t.Fatalf("KeymapConfigFromConfigValues error = %v", err)
	}
	if bindings, ok := config.Binding("global", "open_agents"); !ok || strings.Join(bindings, ",") != "alt-a" {
		t.Fatalf("open_agents bindings = %#v ok=%v", bindings, ok)
	}
	if bindings, ok := config.Binding("agents", "search"); !ok || strings.Join(bindings, ",") != "ctrl-l" {
		t.Fatalf("agents.search bindings = %#v ok=%v", bindings, ok)
	}
	if bindings, ok := config.Binding("agents", "stop"); !ok || len(bindings) != 0 {
		t.Fatalf("agents.stop bindings = %#v ok=%v, want explicit unbind", bindings, ok)
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
		// Rust normalizes Ctrl+5 to Ctrl+] for matching and conflicts #42889.
		"ctrl-5": "ctrl-]",
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

// Rust #54766 removed serde's `deny_unknown_fields` from the keymap structs:
// unknown contexts, an action misplaced at the keymap root, and unknown actions
// are ignored during deserialization, leaving strict-config validation to report
// them. A known context that is not a table is still a type error.
func TestKeymapConfigIgnoresUnknownAndMisplacedActionsLikeRust(t *testing.T) {
	ignored := []struct {
		input  string
		ctx    string
		action string
	}{
		{"[tui.keymap]\nopen_transcript = \"ctrl-s\"\n", "global", "open_transcript"},
		{"[tui.keymap.global]\nopen_transcrip = \"ctrl-s\"\n", "global", "open_transcrip"},
		{"[tui.keymap.vim_text_object]\ndouble_quotes = \"shift-quote\"\n", "vim_text_object", "double_quotes"},
		{"[tui.keymap.future_context]\nanything = \"ctrl-s\"\n", "future_context", "anything"},
	}
	for _, testCase := range ignored {
		var values map[string]any
		if err := toml.Unmarshal([]byte(testCase.input), &values); err != nil {
			t.Fatalf("toml.Unmarshal error = %v", err)
		}
		config, err := KeymapConfigFromConfigValues(values)
		if err != nil {
			t.Fatalf("KeymapConfigFromConfigValues(%q) error = %v, want the unknown field ignored", testCase.input, err)
		}
		if bindings, ok := config.Binding(testCase.ctx, testCase.action); ok {
			t.Fatalf("unknown field produced bindings %#v", bindings)
		}
	}

	var values map[string]any
	if err := toml.Unmarshal([]byte("[tui.keymap]\nglobal = \"ctrl-s\"\n"), &values); err != nil {
		t.Fatalf("toml.Unmarshal error = %v", err)
	}
	if _, err := KeymapConfigFromConfigValues(values); err == nil || !strings.Contains(err.Error(), "tui.keymap.global must be a table") {
		t.Fatalf("non-table context error = %v", err)
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

// TestAsyncQuestionKeymapActionsResolveDefaults pins the question navigation
// defaults added with Rust #42889 and the updated edit_queued_message label.
func TestAsyncQuestionKeymapActionsResolveDefaults(t *testing.T) {
	config := NewKeymapConfig()
	for action, want := range map[string]string{
		"edit_queued_message": "alt-up,shift-left",
		"prompt_stack_back":   "alt-down,shift-right",
		"skip_question":       "ctrl-]",
	} {
		bindings, source, custom := ResolvedKeymapBindings(config, "chat", action)
		if custom || source != "default" || strings.Join(bindings, ",") != want {
			t.Fatalf("chat.%s resolved bindings = %#v source=%q custom=%v, want %s", action, bindings, source, custom, want)
		}
	}

	var editDescription string
	for _, action := range KeymapActions(KeymapActionFilter{}) {
		if action.Context == "chat" && action.Action == "edit_queued_message" {
			editDescription = action.Description
		}
	}
	if !strings.Contains(editDescription, "Move up through questions") {
		t.Fatalf("edit_queued_message description = %q", editDescription)
	}
}

// Rust keeps the vim action names (and the separate vim_search context) that
// Go implements as differently named catalog entries. A config written against
// upstream must still bind the Go behavior instead of being ignored (#54766).
func TestKeymapConfigAppliesUpstreamVimActionAliasesLikeRust(t *testing.T) {
	var values map[string]any
	input := `
[tui.keymap.vim_normal]
enter_replace_mode = "shift-r"
find_forward = "f"
till_backward = "shift-t"

[tui.keymap.vim_search]
forward = "ctrl-f"
previous = "ctrl-p"
`
	if err := toml.Unmarshal([]byte(input), &values); err != nil {
		t.Fatalf("toml.Unmarshal error = %v", err)
	}
	config, err := KeymapConfigFromConfigValues(values)
	if err != nil {
		t.Fatalf("KeymapConfigFromConfigValues error = %v", err)
	}
	for _, testCase := range []struct {
		context string
		action  string
		want    string
	}{
		{"vim_normal", "replace_mode", "shift-r"},
		{"vim_normal", "find_char_forward", "f"},
		{"vim_normal", "till_char_backward", "shift-t"},
		{"vim_normal", "search_forward", "ctrl-f"},
		{"vim_normal", "search_prev", "ctrl-p"},
	} {
		bindings, ok := config.Binding(testCase.context, testCase.action)
		if !ok || len(bindings) != 1 || bindings[0] != testCase.want {
			t.Fatalf("%s.%s bindings = %#v ok=%v, want %q", testCase.context, testCase.action, bindings, ok, testCase.want)
		}
	}
}

// An alias and its canonical Go action must not silently fight over the same
// target; the loader reports the conflict instead of letting map order decide.
func TestKeymapConfigRejectsAliasCanonicalConflictLikeRust(t *testing.T) {
	var values map[string]any
	input := `
[tui.keymap.vim_normal]
replace_mode = "shift-r"
enter_replace_mode = "ctrl-r"
`
	if err := toml.Unmarshal([]byte(input), &values); err != nil {
		t.Fatalf("toml.Unmarshal error = %v", err)
	}
	if _, err := KeymapConfigFromConfigValues(values); err == nil {
		t.Fatal("expected the alias/canonical conflict to be reported")
	}
}
