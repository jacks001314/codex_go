package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/markdown_render/math.rs's recognition helpers,
// including #48551's `$0$` allowance. Go has no math layout engine yet, so this
// file carries the parts of the recogniser that do not depend on it: the
// delimiter classification, the escape test and the inline-formula admission
// heuristic. Code spans, links, HTML and nested containers keep their own
// Markdown rendering.

// maxMathBytes mirrors Rust's `MAX_MATH_BYTES`.
const maxMathBytes = 4096

// mathEscaped reports whether the delimiter at offset is escaped by an odd
// number of immediately preceding backslashes (Rust's `escaped`).
func mathEscaped(input string, offset int) bool {
	if offset < 0 || offset > len(input) {
		return false
	}
	count := 0
	for index := offset - 1; index >= 0 && input[index] == '\\'; index-- {
		count++
	}
	return count%2 == 1
}

// mathOpenDelimiter classifies the math opener starting at rest, in Rust's
// order: `$$` and `\[` are display, `\(` and `$` are inline.
func mathOpenDelimiter(rest string) (open string, close string, display bool, ok bool) {
	switch {
	case strings.HasPrefix(rest, "$$"):
		return "$$", "$$", true, true
	case strings.HasPrefix(rest, `\[`):
		return `\[`, `\]`, true, true
	case strings.HasPrefix(rest, `\(`):
		return `\(`, `\)`, false, true
	case strings.HasPrefix(rest, "$"):
		return "$", "$", false, true
	default:
		return "", "", false, false
	}
}

// inlineMathAdmitted mirrors the `$`-delimited admission rules: a formula that
// ends in whitespace, is followed by an alphanumeric rune, is a bare number
// (except the literal `0`, #48551) or is an all-uppercase initialism is prose,
// not math.
func inlineMathAdmitted(formula string, next rune, hasNext bool) bool {
	if mathFormulaTrailingWhitespace(formula) {
		return false
	}
	if hasNext && isAlnumRune(next) {
		return false
	}
	if formula != "0" && startsWithASCIIDigit(formula) && !strings.ContainsAny(formula, `\^_=+-*/<>`) {
		return false
	}
	return !(len(formula) > 1 && allASCIIUppercase(formula))
}

// mathFormulaTrailingWhitespace reports whether a formula ends in whitespace
// (Rust's `formula.ends_with(char::is_whitespace)`).
func mathFormulaTrailingWhitespace(formula string) bool {
	if formula == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(formula)
	return unicode.IsSpace(last)
}

// isAlnumRune mirrors Rust's `char::is_alphanumeric`.
func isAlnumRune(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsNumber(character)
}

func startsWithASCIIDigit(formula string) bool {
	if formula == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(formula)
	return first >= '0' && first <= '9'
}

func allASCIIUppercase(formula string) bool {
	for _, character := range formula {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}
