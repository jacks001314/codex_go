package tea

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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
	// Rust #58412 paste_input.rs: only a paste that inserts a `!` at the start of
	// the body starts shell mode; a paste anywhere else leaves the draft literal.
	composerBefore := m.composer.Value()
	insertsShellPrefix := m.composerCursorByteOffset()-m.composerPrefix.len() == 0 && strings.HasPrefix(pasted, "!")
	text, blockquote := composerBlockquotePaste(
		composerCursorLinePrefix(&m.composer),
		pasted,
		m.isBangShellCommand(),
	)
	// Rust LARGE_PASTE_CHAR_THRESHOLD (chat_composer.rs:471) is measured on the
	// text that would be inserted, so a blockquote paste counts its added `> `
	// markers (blockquote_paste_large_expands_quoted_text_on_submit). A paste
	// past the threshold becomes an element placeholder and the text is kept in
	// pending_pastes until the draft is submitted.
	content := pasted
	if blockquote {
		content = text
	}
	if charCount := utf8.RuneCountInString(content); charCount > largePasteCharThreshold {
		m.insertLargeComposerPaste(content, charCount)
		if blockquote {
			m.composer.InsertString("\n\n")
		}
		return
	}
	m.composer.InsertString(text)
	if blockquote {
		// The trailing newlines are deliberately unquoted: they leave the cursor
		// in the next Markdown block instead of extending the quote.
		m.composer.InsertString("\n\n")
	}
	if insertsShellPrefix {
		m.syncComposerPrefixAfterEdit(composerBefore)
	}
	if m.modal == nil {
		m.recordComposerActivityAt(m.currentTime())
	}
}

// composerBlockquotePaste decides whether a paste continues the current
// blockquote marker and returns the text to insert.
//
// Rust normalizes CRLF pairs and bare CRs to LF before classifying a paste, so
// the same normalization gates both the "is this multiline?" test and the
// continuation itself. Pastes that are not blockquotes are inserted unchanged.
//
// Rust #52418: one trailing newline is dropped from nonempty content before the
// quote prefixes are added, otherwise the paste ends with an empty quoted line
// before the next Markdown block. Interior blank lines, an all-blank paste, and
// the two unquoted newlines appended by insertComposerPaste are all preserved.
func composerBlockquotePaste(currentLine string, pasted string, bangShellCommand bool) (string, bool) {
	normalized := bottompane.NormalizePastedText(pasted)
	if bangShellCommand || !strings.HasPrefix(currentLine, "> ") || !strings.Contains(normalized, "\n") {
		return pasted, false
	}
	if trimmed := strings.TrimSuffix(normalized, "\n"); trimmed != "" {
		normalized = trimmed
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

// isBangShellCommand mirrors Rust's `ChatComposer::is_bang_shell_command`: the
// composer's shell-mode state decides, not the visible text, so a `!` exposed by
// deleting the start of an ordinary message stays literal (#58412). Rust
// additionally requires `shell_commands_enabled`; the Go model has no
// shell-command toggle, so the prefix state is the whole gate.
func (m *Model) isBangShellCommand() bool {
	if m == nil {
		return false
	}
	return m.composerPrefix == composerPrefixShell
}

// literalBangInput reports whether a submitted draft is ordinary text that only
// happens to start with `!` (Rust #58412: shell mode starts when an edit inserts
// a leading `!`). It reuses the literal-input channel so a deleted prefix cannot
// turn the message into a shell command.
func literalBangInput(input string, shellMode bool) bool {
	return !shellMode && strings.HasPrefix(input, "!")
}

// largePasteCharThreshold mirrors Rust LARGE_PASTE_CHAR_THRESHOLD
// (codex-rs/tui/src/bottom_pane/chat_composer.rs:471). Pastes longer than this
// are stored in pending_pastes instead of being inserted inline.
const largePasteCharThreshold = 1000

// composerPendingPaste mirrors Rust ChatComposer::pending_pastes: the
// placeholder visible in the composer and the text it stands for.
type composerPendingPaste struct {
	Placeholder string
	Content     string
}

// nextLargePastePlaceholder mirrors Rust next_large_paste_placeholder
// (chat_composer.rs:1991-2014): the base label counts characters, a repeated
// size takes the next free " #N" suffix, and a size whose placeholders are all
// gone starts over at the base label.
func (m *Model) nextLargePastePlaceholder(charCount int) string {
	base := fmt.Sprintf("[Pasted Content %d chars]", charCount)
	prefix := base + " #"
	maxSuffix := 0
	if m != nil {
		for _, paste := range m.pendingPastes {
			if paste.Placeholder == base {
				if maxSuffix < 1 {
					maxSuffix = 1
				}
				continue
			}
			suffix, ok := strings.CutPrefix(paste.Placeholder, prefix)
			if !ok {
				continue
			}
			value, err := strconv.Atoi(suffix)
			if err == nil && value > maxSuffix {
				maxSuffix = value
			}
		}
	}
	if maxSuffix == 0 {
		return base
	}
	return fmt.Sprintf("%s #%d", base, maxSuffix+1)
}

// insertLargeComposerPaste replaces a large paste with its placeholder element
// and keeps the text for submission (Rust apply_paste -> pending_pastes).
func (m *Model) insertLargeComposerPaste(pasted string, charCount int) {
	placeholder := m.nextLargePastePlaceholder(charCount)
	insertAt := m.composerCursorByteOffset() - m.composerPrefix.len()
	m.composer.InsertString(placeholder)
	value := m.composer.Value()
	if insertAt < 0 || insertAt+len(placeholder) > len(value) || value[insertAt:insertAt+len(placeholder)] != placeholder {
		insertAt = len(value) - len(placeholder)
	}
	m.registerComposerElement(value, insertAt, insertAt+len(placeholder))
	m.pendingPastes = append(m.pendingPastes, composerPendingPaste{Placeholder: placeholder, Content: pasted})
}

// expandPendingPastesForSubmission mirrors Rust's submit path
// (user_messages.rs:331-354): every pending paste is spliced back into the
// message text, its element is dropped, and the surviving element ranges move
// with the inserted text.
func (m *Model) expandPendingPastesForSubmission(prompt string, elements []ComposerTextElement) (string, []ComposerTextElement) {
	if m == nil || len(m.pendingPastes) == 0 {
		return prompt, elements
	}
	type pasteSpan struct {
		start   int
		end     int
		content string
	}
	spans := make([]pasteSpan, 0, len(m.pendingPastes))
	for _, paste := range m.pendingPastes {
		if paste.Placeholder == "" {
			continue
		}
		index := strings.Index(prompt, paste.Placeholder)
		if index < 0 {
			continue
		}
		spans = append(spans, pasteSpan{start: index, end: index + len(paste.Placeholder), content: paste.Content})
	}
	if len(spans) == 0 {
		return prompt, elements
	}
	sort.SliceStable(spans, func(i int, j int) bool { return spans[i].start < spans[j].start })
	ordered := spans[:0]
	lastEnd := -1
	for _, span := range spans {
		if span.start < lastEnd {
			continue
		}
		ordered = append(ordered, span)
		lastEnd = span.end
	}
	spans = ordered

	var builder strings.Builder
	cursor := 0
	for _, span := range spans {
		builder.WriteString(prompt[cursor:span.start])
		builder.WriteString(span.content)
		cursor = span.end
	}
	builder.WriteString(prompt[cursor:])
	expanded := builder.String()
	// Rust expands pending_pastes first and only then applies trim_submission to
	// the expanded text (chat_composer.rs prepare_submission_text_with_options),
	// so a paste that sits at a draft edge loses its own edge whitespace. Trim
	// here and rebase the surviving element ranges onto the trimmed text.
	submission := strings.TrimSpace(expanded)
	leading := len(expanded) - len(strings.TrimLeftFunc(expanded, unicode.IsSpace))
	shift := func(offset int) int {
		delta := 0
		for _, span := range spans {
			if span.end <= offset {
				delta += len(span.content) - (span.end - span.start)
			}
		}
		return offset + delta
	}
	out := make([]ComposerTextElement, 0, len(elements))
	for _, element := range elements {
		isPasteElement := false
		for _, span := range spans {
			if element.Start == span.start && element.End == span.end {
				isPasteElement = true
				break
			}
		}
		if isPasteElement {
			continue
		}
		start := shift(element.Start) - leading
		end := shift(element.End) - leading
		if start < 0 || end > len(submission) || start >= end || submission[start:end] != element.Placeholder {
			continue
		}
		out = append(out, ComposerTextElement{Start: start, End: end, Placeholder: element.Placeholder})
	}
	if len(out) == 0 {
		return submission, nil
	}
	return submission, out
}

// restorePendingPastes re-attaches a restored draft's large-paste mapping
// (Rust restores ThreadComposerState::pending_pastes with the draft). A mapping
// whose placeholder no longer appears in the draft is dropped.
func (m *Model) restorePendingPastes(pastes []composerPendingPaste) {
	if m == nil {
		return
	}
	m.pendingPastes = nil
	value := m.composer.Value()
	for _, paste := range pastes {
		if paste.Placeholder == "" {
			continue
		}
		index := strings.Index(value, paste.Placeholder)
		if index < 0 {
			continue
		}
		m.pendingPastes = append(m.pendingPastes, paste)
		m.registerComposerElement(value, index, index+len(paste.Placeholder))
	}
}
