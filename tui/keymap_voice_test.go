package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestVoiceMuteDefaultBinding(t *testing.T) {
	bindings, source, custom := ResolvedKeymapBindings(nil, "chat", "toggle_voice_mute")
	if !reflect.DeepEqual(bindings, []string{"ctrl-x"}) {
		t.Fatalf("default bindings = %#v", bindings)
	}
	if source != "default" || custom {
		t.Fatalf("source = %q custom = %v", source, custom)
	}
	if !KeymapActionHasBinding(nil, "chat", "toggle_voice_mute", "ctrl-x") {
		t.Fatal("ctrl-x does not resolve to the voice mute action")
	}
}

func TestVoiceMuteDefaultYieldsToMainSurfaceCtrlX(t *testing.T) {
	// A user binding on a main-surface context shadows the new default.
	actions := map[string]string{
		"global":          "copy",
		"chat":            "interrupt_turn",
		"composer":        "queue",
		"editor":          "move_left",
		"vim_normal":      "enter_insert",
		"vim_operator":    "delete_line",
		"vim_text_object": "word",
	}
	for context, action := range actions {
		t.Run(context, func(t *testing.T) {
			config := NewKeymapConfig()
			if err := config.Set(context, action, []string{"ctrl-x"}); err != nil {
				t.Fatal(err)
			}
			bindings, _, _ := ResolvedKeymapBindings(config, "chat", "toggle_voice_mute")
			if len(bindings) != 0 {
				t.Fatalf("ctrl-x on %s did not shadow the default: %#v", context, bindings)
			}
		})
	}
}

func TestVoiceMuteDefaultSurvivesUnrelatedContextBindings(t *testing.T) {
	// The agents surface consumes its own keys, so its ctrl-x does not shadow
	// the main-surface voice mute shortcut.
	config := NewKeymapConfig()
	if err := config.Set("agents", "stop", []string{"ctrl-x"}); err != nil {
		t.Fatal(err)
	}
	bindings, _, _ := ResolvedKeymapBindings(config, "chat", "toggle_voice_mute")
	if !reflect.DeepEqual(bindings, []string{"ctrl-x"}) {
		t.Fatalf("bindings = %#v", bindings)
	}
}

func TestVoiceMuteExplicitBindingWins(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("chat", "toggle_voice_mute", []string{"ctrl-v"}); err != nil {
		t.Fatal(err)
	}
	bindings, source, custom := ResolvedKeymapBindings(config, "chat", "toggle_voice_mute")
	if !reflect.DeepEqual(bindings, []string{"ctrl-v"}) || source != "custom" || !custom {
		t.Fatalf("bindings = %#v source = %q custom = %v", bindings, source, custom)
	}
}

// Rust #46071: the voice-conversation toggle defaults to F8 and is remappable
// and unbindable like every other configurable action.
func TestVoiceToggleDefaultBinding(t *testing.T) {
	bindings, source, custom := ResolvedKeymapBindings(nil, "chat", "toggle_voice")
	if !reflect.DeepEqual(bindings, []string{"f8"}) {
		t.Fatalf("default bindings = %#v", bindings)
	}
	if source != "default" || custom {
		t.Fatalf("source = %q custom = %v", source, custom)
	}
	if !KeymapActionHasBinding(nil, "chat", "toggle_voice", "f8") {
		t.Fatal("f8 does not resolve to the voice toggle action")
	}

	config := NewKeymapConfig()
	if err := config.Set("chat", "toggle_voice", []string{"ctrl-v"}); err != nil {
		t.Fatal(err)
	}
	if bindings, source, custom := ResolvedKeymapBindings(config, "chat", "toggle_voice"); !reflect.DeepEqual(bindings, []string{"ctrl-v"}) || source != "custom" || !custom {
		t.Fatalf("custom bindings = %#v source = %q custom = %v", bindings, source, custom)
	}

	unbound := NewKeymapConfig()
	if err := unbound.Set("chat", "toggle_voice", nil); err != nil {
		t.Fatal(err)
	}
	if bindings, _, _ := ResolvedKeymapBindings(unbound, "chat", "toggle_voice"); len(bindings) != 0 {
		t.Fatalf("unbound bindings = %#v", bindings)
	}
}

// Rust #46071: the F8 default yields to an existing main-surface F8 binding, so
// the new default never shadows a shortcut the user already configured.
func TestVoiceToggleDefaultYieldsToMainSurfaceF8(t *testing.T) {
	actions := map[string]string{
		"global":          "copy",
		"chat":            "interrupt_turn",
		"composer":        "queue",
		"editor":          "move_left",
		"vim_normal":      "enter_insert",
		"vim_operator":    "delete_line",
		"vim_text_object": "word",
	}
	for context, action := range actions {
		t.Run(context, func(t *testing.T) {
			config := NewKeymapConfig()
			if err := config.Set(context, action, []string{"f8"}); err != nil {
				t.Fatal(err)
			}
			bindings, _, _ := ResolvedKeymapBindings(config, "chat", "toggle_voice")
			if len(bindings) != 0 {
				t.Fatalf("f8 on %s did not shadow the default: %#v", context, bindings)
			}
		})
	}
}

// The voice toggle takes part in the keybinding conflict checks.
func TestVoiceToggleBindingConflictsAreRejected(t *testing.T) {
	config := NewKeymapConfig()
	if err := config.Set("chat", "toggle_voice", []string{"esc"}); err != nil {
		t.Fatal(err)
	}
	err := config.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a binding shared with another chat action")
	}
	if !strings.Contains(err.Error(), "toggle_voice") {
		t.Fatalf("Validate() error = %v, want it to name the toggle_voice action", err)
	}
}
