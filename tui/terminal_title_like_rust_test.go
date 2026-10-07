package tui

import "testing"

// Rust #50375, `terminal_title.rs::screen_title_stays_ascii_across_activity_and_user_named_fields`:
// under GNU Screen the finished title is printable ASCII, while every other
// terminal keeps the Unicode title untouched.
func TestTerminalTitleUsesAsciiUnderScreenLikeRust(t *testing.T) {
	title := " \u25cf \u2807 | \u011b[31m thread \u202e\x07| \u65e5\u672c\u8a9e \u0107af\u00e9  "
	screen := SanitizeTerminalTitleForEncoding(title, TitleEncodingScreen)
	unicode := SanitizeTerminalTitleForEncoding(title, TitleEncodingUnicode)

	if want := "* | | ?[31m thread | ??? ?af?"; screen != want {
		t.Fatalf("Screen title = %q, want %q", screen, want)
	}
	if want := "\u25cf \u2807 | \u011b[31m thread | \u65e5\u672c\u8a9e \u0107af\u00e9"; unicode != want {
		t.Fatalf("Unicode title = %q, want %q", unicode, want)
	}
	for _, r := range screen {
		if r > 0x7f {
			t.Fatalf("Screen title kept a non-ASCII rune %q in %q", r, screen)
		}
	}

	if got := SanitizeTerminalTitleForEncoding("\u65e5\u672c\u8a9e", TitleEncodingScreen); got != "???" {
		t.Fatalf("non-ASCII only title = %q, want %q", got, "???")
	}
	if got := SanitizeTerminalTitleForEncoding("\x07\x1b\u202e\ufeff", TitleEncodingScreen); got != "" {
		t.Fatalf("control-only title = %q, want empty", got)
	}
}

// Rust #50375, `terminal_title.rs::sanitizes_terminal_title`: the encoding only
// changes non-ASCII content, while control characters and bidi formatting are
// still removed.
func TestTerminalTitleSanitizesControlCharactersLikeRust(t *testing.T) {
	sanitized := SanitizeTerminalTitle("  Project\t|\nWorking\x1b\x07\u009d\u009c |  Thread  ")
	if want := "Project | Working | Thread"; sanitized != want {
		t.Fatalf("sanitized = %q, want %q", sanitized, want)
	}
	stripped := SanitizeTerminalTitle("Pro\u202ej\u2066e\u200fc\u061ct\u200b \ufeffT\u2060itle")
	if want := "Project Title"; stripped != want {
		t.Fatalf("stripped = %q, want %q", stripped, want)
	}
}

// Rust #50375: Screen is detected through STY, because TERM=screen alone is not
// sufficient (tmux also uses it).
func TestTerminalTitleEncodingDetectsGnuScreenLikeRust(t *testing.T) {
	t.Setenv("STY", "")
	if got := TerminalTitleEncoding(); got != TitleEncodingUnicode {
		t.Fatalf("encoding without STY = %v, want Unicode", got)
	}
	t.Setenv("STY", "1234.pts-0.host")
	if got := TerminalTitleEncoding(); got != TitleEncodingScreen {
		t.Fatalf("encoding with STY = %v, want Screen", got)
	}
	osc, ok := TerminalTitleOSC("\u25cf \u65e5\u672c\u8a9e")
	if !ok || osc != "\x1b]0;* ???\x07" {
		t.Fatalf("Screen OSC = %q ok=%v", osc, ok)
	}
	t.Setenv("STY", "")
	if osc, ok := TerminalTitleOSC("\u25cf"); !ok || osc != "\x1b]0;\u25cf\x07" {
		t.Fatalf("Unicode OSC = %q ok=%v", osc, ok)
	}
}
