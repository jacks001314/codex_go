package tui

import (
	"reflect"
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
