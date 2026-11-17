package markdown

import (
	"strings"
	"unicode"

	codextui "codex_go/tui"
)

// Rust parity: `RtOptions::break_words` (default true in wrapping.rs).
//
// The Rust renderer lays every block out with `word_wrap_line`, which breaks a
// word that cannot fit on its own line. Go renders Markdown through glamour,
// which word-wraps prose but leaves an over-long token intact, and
// `restoreSourceCodeBlocks` rewrites code lines verbatim. Those rows used to
// reach the transcript viewport wider than the pane, where `ansi.Cut` clipped
// them: a provider error or JSON blob lost its tail instead of wrapping.

// styledWrapToken is one cell of a rendered row. Escape sequences carry no
// width so the wrapper can measure text without parsing SGR state twice.
type styledWrapToken struct {
	raw   string
	width int
}

// wrapRenderedOverflowRows wraps every rendered row that is wider than width.
func wrapRenderedOverflowRows(rendered string, width int) string {
	if width <= 0 || !strings.Contains(rendered, "\n") && codextui.DisplayWidth(rendered) <= width {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	wrapped := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if codextui.DisplayWidth(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}
		changed = true
		wrapped = append(wrapped, wrapRenderedRow(line, width)...)
	}
	if !changed {
		return rendered
	}
	return strings.Join(wrapped, "\n")
}

// wrapRenderedRow breaks one over-wide rendered row into rows no wider than
// limit. The row's leading indentation is repeated on continuation rows, which
// is what Rust's `subsequent_indent` does for code blocks and list items.
func wrapRenderedRow(row string, limit int) []string {
	indent := leadingSpaces(row)
	indentWidth := codextui.DisplayWidth(indent)
	inner := limit - indentWidth
	if inner < 1 {
		// The indentation alone fills the row; give the text the whole width and
		// drop the indent rather than emitting empty rows.
		indent = ""
		inner = limit
	}
	tokens := styledWrapTokens(row[len(indent):])
	chunks := styledWrapChunks(tokens, markURLTokens(tokens), inner)
	if len(chunks) <= 1 {
		return []string{row}
	}
	return renderStyledChunks(indent, tokens, chunks)
}

func leadingSpaces(row string) string {
	end := 0
	for end < len(row) && row[end] == ' ' {
		end++
	}
	return row[:end]
}

// styledWrapTokens splits a rendered row into escape sequences (zero width) and
// single runes so width measurement matches what the terminal paints.
func styledWrapTokens(text string) []styledWrapToken {
	tokens := make([]styledWrapToken, 0, len(text))
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			end := escapeSequenceEnd(text, index)
			if end > index {
				tokens = append(tokens, styledWrapToken{raw: text[index:end]})
				index = end
				continue
			}
		}
		size := runeLen(text[index:])
		raw := text[index : index+size]
		tokens = append(tokens, styledWrapToken{raw: raw, width: codextui.DisplayWidth(raw)})
		index += size
	}
	return tokens
}

// escapeSequenceEnd returns the byte offset just past the escape sequence that
// starts at index, or index when the bytes are not a recognised sequence.
func escapeSequenceEnd(text string, index int) int {
	if index+1 >= len(text) {
		return index
	}
	switch text[index+1] {
	case '[':
		for end := index + 2; end < len(text); end++ {
			if text[end] >= 0x40 && text[end] <= 0x7e {
				return end + 1
			}
		}
		return index
	case ']':
		for end := index + 2; end < len(text); end++ {
			if text[end] == 0x07 {
				return end + 1
			}
			if text[end] == 0x1b && end+1 < len(text) && text[end+1] == '\\' {
				return end + 2
			}
		}
		return index
	default:
		return index + 2
	}
}

func runeLen(text string) int {
	for size := 1; size < len(text) && size < 4; size++ {
		if text[size]&0xC0 != 0x80 {
			return size
		}
	}
	return len(text)
}

// markURLTokens flags the tokens that belong to a URL-like word. Rust's
// url_preserving_wrap_options never splits such a token, so an over-long URL
// keeps its own over-wide row instead of being broken mid-name (#51449).
func markURLTokens(tokens []styledWrapToken) []bool {
	noSplit := make([]bool, len(tokens))
	plain := make([]rune, 0, len(tokens))
	runeToken := make([]int, 0, len(tokens))
	for index, token := range tokens {
		if token.width == 0 && strings.HasPrefix(token.raw, "\x1b") {
			continue
		}
		for _, r := range token.raw {
			plain = append(plain, r)
			runeToken = append(runeToken, index)
		}
	}
	start := -1
	for index := 0; index <= len(plain); index++ {
		space := index == len(plain) || unicode.IsSpace(plain[index])
		if !space && start < 0 {
			start = index
		}
		if space && start >= 0 {
			if isURLLikeToken(string(plain[start:index])) {
				for _, tokenIndex := range runeToken[start:index] {
					noSplit[tokenIndex] = true
				}
			}
			start = -1
		}
	}
	return noSplit
}

// isURLLikeToken mirrors Rust's is_url_like_token: a scheme://host form or a
// bare www./domain host with a URL-ish trailer.
func isURLLikeToken(token string) bool {
	token = strings.Trim(token, "()[]{}<>,.;:!'\"")
	if token == "" {
		return false
	}
	if scheme, rest, ok := strings.Cut(token, "://"); ok {
		if scheme == "" || rest == "" {
			return false
		}
		for index, r := range scheme {
			if index == 0 {
				if !unicode.IsLetter(r) || r > 0x7e {
					return false
				}
				continue
			}
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' && r != '-' && r != '.' {
				return false
			}
		}
		return true
	}
	lower := strings.ToLower(token)
	if strings.HasPrefix(lower, "www.") {
		return strings.Contains(lower, ".")
	}
	return false
}

// styledWrapChunks greedily fills rows of at most width cells, preferring the
// last space of the row as the break point so ordinary words stay whole. A
// break requested inside a URL is moved past the URL so the token stays whole.
func styledWrapChunks(tokens []styledWrapToken, noSplit []bool, width int) [][2]int {
	if width < 1 {
		width = 1
	}
	chunks := make([][2]int, 0, 2)
	start := 0
	for start < len(tokens) {
		used := 0
		lastSpace := -1
		index := start
		for index < len(tokens) {
			token := tokens[index]
			if used+token.width > width {
				break
			}
			used += token.width
			if token.width > 0 && token.raw == " " {
				lastSpace = index
			}
			index++
		}
		if index >= len(tokens) {
			chunks = append(chunks, [2]int{start, len(tokens)})
			break
		}
		breakAt := index
		if lastSpace > start {
			breakAt = lastSpace
		}
		for breakAt < len(tokens) && noSplit[breakAt] {
			breakAt++
		}
		if breakAt == start {
			// A single cell is wider than the row (a double-width rune in a
			// one-column pane): emit it alone instead of looping forever.
			breakAt = start + 1
		}
		chunks = append(chunks, [2]int{start, breakAt})
		start = breakAt
		if start < len(tokens) && tokens[start].raw == " " {
			start++
		}
	}
	return chunks
}

// renderStyledChunks paints the chunks, re-applying the SGR attributes and the
// OSC-8 link that are active at each break so a styled or linked token keeps its
// appearance on the continuation rows.
func renderStyledChunks(indent string, tokens []styledWrapToken, chunks [][2]int) []string {
	starts := make(map[int]int, len(chunks))
	for index, chunk := range chunks {
		starts[chunk[0]] = index
	}
	patterns := make([]string, len(chunks))
	links := make([]string, len(chunks))
	sgr := make([]string, 0, 4)
	link := ""
	for index, token := range tokens {
		if chunk, ok := starts[index]; ok {
			patterns[chunk] = strings.Join(sgr, "")
			links[chunk] = link
		}
		switch {
		case isSGRSequence(token.raw):
			if sgrResetsAll(token.raw) {
				sgr = sgr[:0]
			} else {
				sgr = append(sgr, token.raw)
			}
		case isHyperlinkOpen(token.raw):
			link = token.raw
		case token.raw == "\x1b]8;;\x07" || token.raw == "\x1b]8;;\x1b\\":
			link = ""
		}
	}
	rows := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		var sb strings.Builder
		sb.WriteString(indent)
		sb.WriteString(patterns[index])
		sb.WriteString(links[index])
		for _, token := range tokens[chunk[0]:chunk[1]] {
			sb.WriteString(token.raw)
		}
		if patterns[index] != "" {
			sb.WriteString("\x1b[0m")
		}
		if links[index] != "" {
			sb.WriteString("\x1b]8;;\x07")
		}
		rows = append(rows, strings.TrimRight(sb.String(), " "))
	}
	return rows
}

func isSGRSequence(sequence string) bool {
	return strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m")
}

func isHyperlinkOpen(sequence string) bool {
	if !strings.HasPrefix(sequence, "\x1b]8;;") {
		return false
	}
	return !strings.HasSuffix(sequence, "\x1b]8;;\x07") && !strings.HasSuffix(sequence, "\x1b]8;;\x1b\\")
}

func sgrResetsAll(sequence string) bool {
	parameters := sequence[2 : len(sequence)-1]
	if parameters == "" {
		return true
	}
	for _, parameter := range strings.Split(parameters, ";") {
		if strings.Trim(parameter, "0") != "" {
			return false
		}
	}
	return true
}
