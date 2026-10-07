package tui

import (
	"strings"
	"testing"
)

// Rust #49804 (d2f2c40095, codex-rs/tui/src/resume_picker.rs footer_hint_lines):
// the session picker's ctrl chords render through the shared key-label table
// instead of hard-coded "ctrl+o / ctrl+t / ctrl+e / ctrl+c" strings, so Linux
// shows ^c/^o/^t/^e while macOS shows the control glyph. Mirrors the Rust
// assertions `footer_hints_render_shifted_labels` and
// `compact_footer_hints_fit_narrow_rows`.
func TestSessionPickerFooterChordsUsePlatformControlLabelLikeRust(t *testing.T) {
	ctrl := ModifierLabelPrefix(ControlKeyLabel())
	state := &SessionPickerState{}

	wide := strings.Join(state.FooterLines(200, false), "\n")
	for _, want := range []string{ctrl + "c quit", ctrl + "o dense view", ctrl + "t transcript", ctrl + "e expand"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide footer missing %q:\n%s", want, wide)
		}
	}
	narrow := strings.Join(state.FooterLines(80, false), "\n")
	for _, want := range []string{ctrl + "c quit", ctrl + "o dense", ctrl + "t preview", ctrl + "e exp"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow footer missing %q:\n%s", want, narrow)
		}
	}
	// Reverse guard: the pre-#49804 hard-coded chord must be gone whenever the
	// platform label is not the textual one.
	if ctrl != "ctrl+" && strings.Contains(wide, "ctrl+o") {
		t.Fatalf("wide footer still hard-codes ctrl+o:\n%s", wide)
	}
	if existing := strings.Join(state.FooterLines(200, true), "\n"); !strings.Contains(existing, ctrl+"c exit") {
		t.Fatalf("existing-session footer missing %q:\n%s", ctrl+"c exit", existing)
	}
}
