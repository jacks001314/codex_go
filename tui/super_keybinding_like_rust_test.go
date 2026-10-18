package tui

import (
	"runtime"
	"testing"
)

// TestNormalizeKeybindingSpecCommandAliasesLikeRust mirrors Rust #53754:
// cmd/command/super share one canonical modifier spelling.
func TestNormalizeKeybindingSpecCommandAliasesLikeRust(t *testing.T) {
	for _, spec := range []string{"cmd-left", "command-left", "super-left"} {
		got, err := NormalizeKeybindingSpec(spec)
		if err != nil || got != "super-left" {
			t.Fatalf("NormalizeKeybindingSpec(%q) = %q, %v; want super-left", spec, got, err)
		}
	}
	got, err := NormalizeKeybindingSpec("ctrl-alt-shift-super-k")
	if err != nil || got != "ctrl-alt-shift-super-k" {
		t.Fatalf("canonical super binding = %q, %v", got, err)
	}
	if _, err := NormalizeKeybindingSpec("left-super"); err == nil {
		t.Fatal("a modifier after the key was accepted")
	}
}

// TestSuperLeaderPrefixAcceptedLikeRust keeps super-chord and leader
// validation from treating the modifier as ordinary text.
func TestSuperLeaderPrefixAcceptedLikeRust(t *testing.T) {
	for _, prefix := range []string{"super-a", "super-left"} {
		if err := ValidateLeaderPrefix(prefix); err != nil {
			t.Fatalf("ValidateLeaderPrefix(%q) = %v", prefix, err)
		}
	}
}

// TestSuperEditorDefaultsAndLabelsLikeRust checks the macOS-style defaults and
// their platform labels.
func TestSuperEditorDefaultsAndLabelsLikeRust(t *testing.T) {
	actions := map[string]KeymapAction{}
	for _, action := range KeymapActions(KeymapActionFilter{}) {
		actions[action.Context+"."+action.Action] = action
	}
	for key, want := range map[string]string{
		"editor.move_line_start": "super-left",
		"editor.move_line_end":   "super-right",
		"editor.kill_line_start": "super-backspace",
	} {
		action, ok := actions[key]
		if !ok {
			t.Fatalf("missing keymap action %s", key)
		}
		found := false
		for _, binding := range action.DefaultBindings {
			if binding == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s bindings = %#v, want %q", key, action.DefaultBindings, want)
		}
	}
	if got := ModifierKeyLabelForPlatform(KeyModifierSuper, "darwin"); got != "\u2318" {
		t.Fatalf("super label on darwin = %q, want \u2318", got)
	}
	if got := ModifierKeyLabelForPlatform(KeyModifierSuper, "linux"); got != "super" {
		t.Fatalf("super label on linux = %q, want super", got)
	}
	want := ModifierLabelPrefix(SuperKeyLabel()) + "\u2190"
	if got := KeybindingDisplayLabel("super-left"); got != want {
		t.Fatalf("super-left label = %q, want %q", got, want)
	}
	if runtime.GOOS == "darwin" && want != "\u2318\u2190" {
		t.Fatalf("macOS super-left label = %q, want \u2318\u2190", want)
	}
}
