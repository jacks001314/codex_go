package historycell

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/tui"
)

// Rust #49395 (ceea67163f, codex-rs/tui/src/history_cell/session.rs): the
// session header no longer binds a randomized startup greeting, so the raw
// lines always carry the model/directory fields while the compact banner never
// does. Mirrors the Rust assertions in `app/tests/startup_frame_tests.rs`
// (text.contains("model:") == raw mode) and
// `empty_state_animation_preserves_header_cursor_and_footer`.
func TestSessionHeaderOmitsRandomizedGreetingLikeRust(t *testing.T) {
	// #49395 removed the shared greeting state outright: the header cell must
	// not carry a greeting field or its binding method any more.
	typ := reflect.TypeOf(SessionHeaderHistoryCell{})
	if _, ok := typ.FieldByName("Greeting"); ok {
		t.Fatal("SessionHeaderHistoryCell still carries a Greeting field (Rust #49395 removed it)")
	}
	if _, ok := typ.MethodByName("WithGreeting"); ok {
		t.Fatal("SessionHeaderHistoryCell still carries WithGreeting (Rust #49395 removed it)")
	}

	header := NewSessionHeader("gpt-5", "high", `D:\repo`, "1.2.3")

	displayLines := header.DisplayLines(80)
	display := strings.Join(displayLines, "\n")
	// Rust #48562: every session header uses the compact borderless layout.
	if !strings.Contains(display, ">_ gcode (v1.2.3)") || !strings.Contains(display, `D:\repo`) {
		t.Fatalf("display header = %q", display)
	}
	if strings.Contains(display, "model:") {
		t.Fatalf("display header must not carry the raw model line:\n%s", display)
	}
	if strings.Contains(display, "\u256d") {
		t.Fatalf("display header must be borderless:\n%s", display)
	}
	for _, line := range displayLines {
		if tui.DisplayWidth(line) > 80 {
			t.Fatalf("display header line exceeds the width: %q", line)
		}
	}

	raw := strings.Join(header.RawLines(), "\n")
	if !strings.Contains(raw, "model: gpt-5 high") || !strings.Contains(raw, "directory: ") {
		t.Fatalf("raw header = %q", raw)
	}

	for _, phrase := range []string{
		"Pull up a prompt.",
		"Whoa, fancy meeting you here!",
		"Look who\u2019s at the keyboard.",
		"A long time ago, in a directory not so far away\u2026",
	} {
		if strings.Contains(display, phrase) || strings.Contains(raw, phrase) {
			t.Fatalf("header carries a startup greeting %q:\ndisplay=%q\nraw=%q", phrase, display, raw)
		}
	}

	yolo := strings.Join(NewSessionHeader("gpt-5", "high", `D:\repo`, "1.2.3").WithYoloMode(true).RawLines(), "\n")
	if !strings.Contains(yolo, "permissions: YOLO mode") {
		t.Fatalf("yolo raw header = %q", yolo)
	}
}
