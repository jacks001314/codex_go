package tui

import "testing"

// Rust #49136, `key_hint_label_tests.rs::modifier_combinations_stay_compact_inside_chords`:
// glyph modifier labels attach to the key without a `+`, while text labels keep
// it, so macOS renders `⌥t` and `ctrl+⌥t` but Linux keeps `alt+t`.
func TestModifierLabelOmitsPlusAfterGlyphsLikeRust(t *testing.T) {
	cases := []struct {
		label string
		want  string
	}{
		{"\u2303", "\u2303"},
		{"\u21e7", "\u21e7"},
		{"\u2325", "\u2325"},
		{"\u2318", "\u2318"},
		{"^", "^"},
		{"ctrl", "ctrl+"},
		{"shift", "shift+"},
		{"alt", "alt+"},
	}
	for _, tc := range cases {
		if got := ModifierLabelPrefix(tc.label); got != tc.want {
			t.Errorf("ModifierLabelPrefix(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}

	if got := ModifierLabelPrefix("\u2325") + "t"; got != "\u2325t" {
		t.Fatalf("option chord = %q, want %q", got, "\u2325t")
	}
	if got := ModifierLabelPrefix("ctrl") + ModifierLabelPrefix("\u2325") + "t"; got != "ctrl+\u2325t" {
		t.Fatalf("ctrl+option chord = %q, want %q", got, "ctrl+\u2325t")
	}
}

// Rust #49136 turned the footer reasoning hints from `⌥+,` into `⌥,`; on macOS
// the option glyph therefore attaches directly, while the `alt` text label used
// everywhere else keeps its `+` separator.
func TestFooterReasoningHintFollowsModifierLabelLikeRust(t *testing.T) {
	want := "alt+,"
	if AltKeyLabel() == "\u2325" {
		want = "\u2325,"
	}
	if got := ModifierLabelPrefix(AltKeyLabel()) + ","; got != want {
		t.Fatalf("reasoning down hint = %q, want %q", got, want)
	}
	wantUp := "alt+."
	if AltKeyLabel() == "\u2325" {
		wantUp = "\u2325."
	}
	if got := ModifierLabelPrefix(AltKeyLabel()) + "."; got != wantUp {
		t.Fatalf("reasoning up hint = %q, want %q", got, wantUp)
	}
}
