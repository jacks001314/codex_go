package tui

import (
	"os"
	"testing"
)

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

// Rust #62573: the emoji presentation selector right after the cloud marker
// survives Unicode titles, while Screen titles stay printable ASCII and a stray
// selector on any other rune is still stripped.
func TestTerminalTitleKeepsCloudEmojiSelectorLikeRust(t *testing.T) {
	title := " \u2601\uFE0F \u25CF \u2807 | thread "
	if got, want := SanitizeTerminalTitleForEncoding(title, TitleEncodingUnicode), "\u2601\uFE0F \u25CF \u2807 | thread"; got != want {
		t.Fatalf("Unicode title = %q, want %q", got, want)
	}
	screen := SanitizeTerminalTitleForEncoding(title, TitleEncodingScreen)
	for _, r := range screen {
		if r > 0x7f {
			t.Fatalf("Screen title kept a non-ASCII rune %q in %q", r, screen)
		}
	}
	if got := SanitizeTerminalTitleForEncoding("\uFE0F ab", TitleEncodingUnicode); got != "ab" {
		t.Fatalf("leading emoji selector = %q, want it stripped", got)
	}
	if got := SanitizeTerminalTitleForEncoding("a\uFE0F b", TitleEncodingUnicode); got != "a b" {
		t.Fatalf("selector after another rune = %q, want it stripped", got)
	}
}

// Rust #61742/#62573: a decorated cloud label keeps its emoji selector when it
// reaches a Unicode terminal title.
func TestCloudThreadLabelSurvivesTerminalTitleLikeRust(t *testing.T) {
	label := DecorateThreadLabel("Fix cache", true)
	if got, want := SanitizeTerminalTitle(label), "\u2601\uFE0F Fix cache"; got != want {
		t.Fatalf("decorated title = %q, want %q", got, want)
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
	formatted := SanitizeTerminalTitle("A\u00a0\u3000B\u200b\u2060C\U0001BCA0D\U000E0100E")
	if want := "A BCDE"; formatted != want {
		t.Fatalf("supplementary formatting = %q, want %q", formatted, want)
	}
}

// Rust #50375: Screen is detected through STY, because TERM=screen alone is not
// sufficient (tmux also uses it).
func TestTerminalTitleEncodingDetectsGnuScreenLikeRust(t *testing.T) {
	t.Setenv("STY", "")
	if got := TerminalTitleEncoding(); got != TitleEncodingScreen {
		t.Fatalf("encoding with an empty STY = %v, want Screen", got)
	}
	if err := os.Unsetenv("STY"); err != nil {
		t.Fatalf("unset STY: %v", err)
	}
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
	if err := os.Unsetenv("STY"); err != nil {
		t.Fatalf("unset STY: %v", err)
	}
	if osc, ok := TerminalTitleOSC("\u25cf"); !ok || osc != "\x1b]0;\u25cf\x07" {
		t.Fatalf("Unicode OSC = %q ok=%v", osc, ok)
	}
}
