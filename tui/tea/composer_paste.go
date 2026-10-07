package tea

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"

	bottompane "codex_go/tui/bottom_pane"
)

// insertComposerPaste integrates pasted text into the composer.
//
// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer/paste_input.rs
// `ChatComposer::apply_paste` (Rust #49357, upstream 1983c48fd1). A multiline
// paste whose insertion point sits on a Markdown blockquote line (`> `) continues
// that marker on every pasted line, and two unquoted newlines are appended so
// typing resumes in the next block. Shell (`!`) input stays literal.
//
// Go keeps a single composer for the main prompt and for the embedded answer
// fields (async questions, request_user_input, MCP elicitation), so this entry
// point covers both surfaces, mirroring the Rust `plain_text()` composers that
// keep blockquote continuation enabled.
func (m *Model) insertComposerPaste(pasted string) {
	if m == nil || pasted == "" {
		return
	}
	text, blockquote := composerBlockquotePaste(
		composerCursorLinePrefix(&m.composer),
		pasted,
		m.isBangShellCommand(),
	)
	m.composer.InsertString(text)
	if blockquote {
		// The trailing newlines are deliberately unquoted: they leave the cursor
		// in the next Markdown block instead of extending the quote.
		m.composer.InsertString("\n\n")
	}
}

// composerBlockquotePaste decides whether a paste continues the current
// blockquote marker and returns the text to insert.
//
// Rust normalizes CRLF pairs and bare CRs to LF before classifying a paste, so
// the same normalization gates both the "is this multiline?" test and the
// continuation itself. Pastes that are not blockquotes are inserted unchanged.
func composerBlockquotePaste(currentLine string, pasted string, bangShellCommand bool) (string, bool) {
	normalized := bottompane.NormalizePastedText(pasted)
	if bangShellCommand || !strings.HasPrefix(currentLine, "> ") || !strings.Contains(normalized, "\n") {
		return pasted, false
	}
	return strings.ReplaceAll(normalized, "\n", "\n> "), true
}

// composerCursorLinePrefix returns the cursor's line up to the cursor, mirroring
// Rust's `&textarea.text()[..target.start()].rsplit('\n').next()`. Go's textarea
// has no mouse selection, so the edit target is always the cursor; Rust uses the
// selection start when one exists instead.
func composerCursorLinePrefix(m *textarea.Model) string {
	if m == nil || m.LineCount() == 0 {
		return ""
	}
	lines := strings.Split(m.Value(), "\n")
	row := m.Line()
	if row < 0 || row >= len(lines) {
		return ""
	}
	runes := []rune(lines[row])
	// StartColumn + ColumnOffset is the cursor's rune offset inside the logical
	// line; wrapping preserves rune counts, so this holds for soft-wrapped lines.
	info := m.LineInfo()
	offset := info.StartColumn + info.ColumnOffset
	switch {
	case offset <= 0:
		return ""
	case offset >= len(runes):
		return string(runes)
	default:
		return string(runes[:offset])
	}
}

// isBangShellCommand mirrors Rust's `ChatComposer::is_bang_shell_command`: a
// draft whose trimmed text starts with `!` is shell input, exactly like the
// `shouldSubmitOnTab` gate. Rust additionally requires `shell_commands_enabled`;
// the Go model has no shell-command toggle, so the prefix is the whole gate.
func (m *Model) isBangShellCommand() bool {
	if m == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(m.composer.Value()), "!")
}
