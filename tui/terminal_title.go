package tui

import (
	"os"
	"strings"
)

// Rust parity: codex-rs/tui/src/terminal_title.rs.

const MaxTerminalTitleChars = 240

// TitleEncoding mirrors Rust's title encoding switch (#50375). GNU Screen
// truncates Unicode in OSC titles to a byte, which can turn a visible character
// into a control byte and spill title text into the terminal, so under Screen the
// whole finished title must be printable ASCII.
type TitleEncoding int

const (
	// TitleEncodingUnicode keeps the title's Unicode content (every terminal
	// except GNU Screen).
	TitleEncodingUnicode TitleEncoding = iota
	// TitleEncodingScreen degrades the title to printable ASCII.
	TitleEncodingScreen
)

// TerminalTitleEncoding reports the encoding Rust selects for the current
// environment: Screen is detected through STY, because TERM=screen alone is not
// sufficient (tmux also commonly uses that TERM, and a multiplexer inside Screen
// still passes the title through Screen).
func TerminalTitleEncoding() TitleEncoding {
	if os.Getenv("STY") != "" {
		return TitleEncodingScreen
	}
	return TitleEncodingUnicode
}

// SanitizeTerminalTitle is the Unicode encoding of
// SanitizeTerminalTitleForEncoding.
func SanitizeTerminalTitle(title string) string {
	return SanitizeTerminalTitleForEncoding(title, TitleEncodingUnicode)
}

// SanitizeTerminalTitleForEncoding removes terminal control characters, strips
// invisible/bidi formatting characters, collapses any whitespace run into a
// single ASCII space, truncates after MaxTerminalTitleChars emitted characters
// and, under Screen, maps the title to printable ASCII (#50375).
func SanitizeTerminalTitleForEncoding(title string, encoding TitleEncoding) string {
	var out strings.Builder
	wrote := 0
	pendingSpace := false
	for _, r := range title {
		if isTitleWhitespace(r) {
			pendingSpace = out.Len() > 0
			continue
		}
		if isDisallowedTerminalTitleRune(r) {
			continue
		}
		r = screenTitleRune(encoding, r)
		if pendingSpace && wrote < MaxTerminalTitleChars-1 {
			out.WriteRune(' ')
			wrote++
			pendingSpace = false
		}
		if wrote >= MaxTerminalTitleChars {
			break
		}
		out.WriteRune(r)
		wrote++
	}
	return out.String()
}

func TerminalTitleOSC(title string) (string, bool) {
	return TerminalTitleOSCForEncoding(title, TerminalTitleEncoding())
}

// TerminalTitleOSCForEncoding builds the OSC 0 sequence for a title under an
// explicit encoding.
func TerminalTitleOSCForEncoding(title string, encoding TitleEncoding) (string, bool) {
	title = SanitizeTerminalTitleForEncoding(title, encoding)
	if title == "" {
		return "", false
	}
	return "\x1b]0;" + title + "\x07", true
}

// screenTitleRune maps the title under GNU Screen to printable ASCII (#50375):
// the activity spinners and the status dot keep a visible equivalent and any
// other non-ASCII content becomes '?'. Unicode titles are untouched elsewhere.
func screenTitleRune(encoding TitleEncoding, r rune) rune {
	if encoding != TitleEncodingScreen {
		return r
	}
	switch r {
	case '\u280b', '\u283c', '\u2807':
		return '|'
	case '\u2819', '\u2834', '\u280f':
		return '/'
	case '\u2839', '\u2826':
		return '-'
	case '\u2838', '\u2827':
		return '\\'
	case '\u25cf':
		return '*'
	}
	if r > 0x7f {
		return '?'
	}
	return r
}

func ClearTerminalTitleOSC() string {
	return "\x1b]0;\x07"
}

func isTitleWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

func isDisallowedTerminalTitleRune(r rune) bool {
	// Rust drops every `char::is_control()` rune, which is the C0 range plus DEL
	// and the C1 range (Rust #50375 keeps the existing sanitizer contract).
	if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
		return true
	}
	return r == 0x00AD ||
		r == 0x034F ||
		r == 0x061C ||
		r == 0x180E ||
		(r >= 0x200B && r <= 0x200F) ||
		(r >= 0x202A && r <= 0x202E) ||
		(r >= 0x2060 && r <= 0x206F) ||
		(r >= 0xFE00 && r <= 0xFE0F) ||
		r == 0xFEFF ||
		(r >= 0xFFF9 && r <= 0xFFFB)
}
