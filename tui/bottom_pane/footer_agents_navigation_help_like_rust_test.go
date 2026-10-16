package bottompane

import (
	"testing"

	"codex_go/tui"
)

// Rust #53153 (bottom_pane/shortcut_overlay.rs:80-88) adds the
// "Subagents (empty prompt)" row to the shortcut overlay's Session group, right
// after the "Agents (empty prompt)" hint and before "Inspect activity", keyed
// by previous_agent_shortcut()/next_agent_shortcut() - the fixed alt(Left) /
// alt(Right) bindings, so the label is the alt modifier label plus the arrow
// glyph. Rust's checks render it as "\u2325\u2190 / \u2325\u2192  Subagents (empty prompt)"
// (bottom_pane/snapshots/..._agents_navigation_help.snap and
// ..._shortcut_overlay_customized_wsl.snap), because the snapshot build prints
// the macOS glyph table.
//
// Go's overlay is a flat list, so this test pins the same row text against the
// same runtime label primitives the other footer rows use.
func TestFooterShortcutOverlayShowsSubagentNavigationLikeRust(t *testing.T) {
	previous := tui.ModifierLabelPrefix(tui.AltKeyLabel()) + "\u2190"
	next := tui.ModifierLabelPrefix(tui.AltKeyLabel()) + "\u2192"
	want := previous + " / " + next + " Subagents (empty prompt)"

	overlay := FooterShortcutOverlayLines(FooterProps{CollaborationModesEnabled: true})
	index := indexOfFooterLine(overlay, want)
	if index < 0 {
		t.Fatalf("shortcut overlay missing %q: %#v", want, overlay)
	}

	// Rust keeps the row inside the Session group, after the reasoning rows and
	// before the transcript group; the flat Go list preserves that relative
	// order (Rust renders the groups side by side).
	reasoningUp := indexOfFooterLine(overlay, tui.ModifierLabelPrefix(tui.AltKeyLabel())+". reasoning up")
	transcript := indexOfFooterLine(overlay, "ctrl+t to view transcript")
	if reasoningUp < 0 || transcript < 0 {
		t.Fatalf("overlay lost its reasoning/transcript rows: %#v", overlay)
	}
	if index < reasoningUp || index > transcript {
		t.Fatalf("subagent row at %d, want between %d and %d: %#v", index, reasoningUp, transcript, overlay)
	}

	// The row is appended, not swapped in: the pre-existing rows stay.
	for _, line := range []string{
		"/ for commands",
		"tab to submit message",
		"ctrl+c to exit",
		"customize shortcuts with /keymap",
	} {
		if indexOfFooterLine(overlay, line) < 0 {
			t.Fatalf("shortcut overlay lost %q: %#v", line, overlay)
		}
	}
}

// The key follows Rust's shared modifier table plus the #49136 separator rule,
// so it matches the platform cells Rust's snapshots capture.
func TestSubagentNavigationRowFollowsPlatformModifierLabelLikeRust(t *testing.T) {
	for _, cell := range []struct {
		goos  string
		label string
	}{
		{"darwin", "\u2325\u2190 / \u2325\u2192 Subagents (empty prompt)"},
		{"linux", "alt+\u2190 / alt+\u2192 Subagents (empty prompt)"},
		{"windows", "alt+\u2190 / alt+\u2192 Subagents (empty prompt)"},
	} {
		modifier := tui.ModifierLabelPrefix(tui.ModifierKeyLabelForPlatform(tui.KeyModifierAlt, cell.goos))
		if got := modifier + "\u2190 / " + modifier + "\u2192 Subagents (empty prompt)"; got != cell.label {
			t.Fatalf("%s subagent row = %q, want %q", cell.goos, got, cell.label)
		}
	}
}

func indexOfFooterLine(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
			return i
		}
	}
	return -1
}
