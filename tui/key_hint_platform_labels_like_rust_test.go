package tui

import (
	"runtime"
	"testing"
)

// Rust #49804 (d2f2c40095, codex-rs/tui/src/key_hint.rs): the hard-coded
// "ctrl"/"shift" shortcut labels are replaced by a platform MODIFIER_LABELS
// table — macOS renders ⌃ / ⇧ / ⌥, Linux renders ^ for control while keeping
// shift / alt textual, and every other platform keeps ctrl / shift / alt.
// Rust asserts the macOS table because it compiles it under
// `cfg(any(test, target_os = "macos"))`; the table is parameterized here so all
// three platforms stay assertable. Mirrors the Rust test
// `modifier_combinations_stay_compact_inside_chords`
// (codex-rs/tui/src/key_hint_label_tests.rs).
func TestModifierKeyLabelsFollowPlatformTableLikeRust(t *testing.T) {
	cases := []struct {
		modifier             KeyModifier
		darwin, linux, other string
	}{
		{KeyModifierControl, "\u2303", "^", "ctrl"},
		{KeyModifierShift, "\u21e7", "shift", "shift"},
		{KeyModifierAlt, "\u2325", "alt", "alt"},
	}
	for _, tc := range cases {
		if got := ModifierKeyLabelForPlatform(tc.modifier, "darwin"); got != tc.darwin {
			t.Errorf("ModifierKeyLabelForPlatform(%s, darwin) = %q, want %q", tc.modifier, got, tc.darwin)
		}
		if got := ModifierKeyLabelForPlatform(tc.modifier, "linux"); got != tc.linux {
			t.Errorf("ModifierKeyLabelForPlatform(%s, linux) = %q, want %q", tc.modifier, got, tc.linux)
		}
		if got := ModifierKeyLabelForPlatform(tc.modifier, "windows"); got != tc.other {
			t.Errorf("ModifierKeyLabelForPlatform(%s, windows) = %q, want %q", tc.modifier, got, tc.other)
		}
	}

	// The host helpers must be the table entry for this platform, so a macOS
	// build renders ⌃t while a Linux build renders ^t.
	if got, want := ControlKeyLabel(), ModifierKeyLabelForPlatform(KeyModifierControl, runtime.GOOS); got != want {
		t.Errorf("ControlKeyLabel() = %q, want %q", got, want)
	}
	if got, want := ShiftKeyLabel(), ModifierKeyLabelForPlatform(KeyModifierShift, runtime.GOOS); got != want {
		t.Errorf("ShiftKeyLabel() = %q, want %q", got, want)
	}
	if got, want := AltKeyLabel(), ModifierKeyLabelForPlatform(KeyModifierAlt, runtime.GOOS); got != want {
		t.Errorf("AltKeyLabel() = %q, want %q", got, want)
	}
	if runtime.GOOS == "linux" && ControlKeyLabel() != "^" {
		t.Errorf("ControlKeyLabel() on linux = %q, want ^ (#49804)", ControlKeyLabel())
	}
}

// Rust #49804 + #49136: `KeyBinding::display_label` renders modifiers in
// control/shift/alt order, and only textual labels keep the `+` separator, so a
// chord is `^t` on Linux and `⌃⇧t` on macOS. Mirrors the Rust test
// `modifier_combinations_stay_compact_inside_chords`.
func TestShortcutChordLabelsPerPlatformLikeRust(t *testing.T) {
	compose := func(goos string, control, shift, alt bool) string {
		label := ""
		if control {
			label += ModifierLabelPrefix(ModifierKeyLabelForPlatform(KeyModifierControl, goos))
		}
		if shift {
			label += ModifierLabelPrefix(ModifierKeyLabelForPlatform(KeyModifierShift, goos))
		}
		if alt {
			label += ModifierLabelPrefix(ModifierKeyLabelForPlatform(KeyModifierAlt, goos))
		}
		return label + "t"
	}

	cases := []struct {
		name                string
		goos                string
		control, shift, alt bool
		want                string
	}{
		{"macos control", "darwin", true, false, false, "\u2303t"},
		{"macos alt", "darwin", false, false, true, "\u2325t"},
		{"macos control+shift", "darwin", true, true, false, "\u2303\u21e7t"},
		{"macos control+alt", "darwin", true, false, true, "\u2303\u2325t"},
		{"linux control", "linux", true, false, false, "^t"},
		{"linux control+alt", "linux", true, false, true, "^alt+t"},
		{"linux alt", "linux", false, false, true, "alt+t"},
		{"windows control", "windows", true, false, false, "ctrl+t"},
		{"windows control+shift", "windows", true, true, false, "ctrl+shift+t"},
	}
	for _, tc := range cases {
		if got := compose(tc.goos, tc.control, tc.shift, tc.alt); got != tc.want {
			t.Errorf("%s chord = %q, want %q", tc.name, got, tc.want)
		}
	}
}
