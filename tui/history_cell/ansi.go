package historycell

// ANSI SGR parsing for hook system messages. Rust parity: the
// codex-ansi-escape crate (a thin wrapper over ansi_to_tui) used by
// history_cell::hook_cell to turn hook output into styled spans.

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"codex_go/tui"
)

// ParseANSISpans converts text carrying ANSI SGR escape sequences into styled
// spans, dropping the escape sequences themselves. Unsupported escape forms are
// dropped without leaking control bytes into the rendered text.
func ParseANSISpans(text string) []StyledSpan {
	if !strings.ContainsRune(text, 0x1b) {
		if text == "" {
			return nil
		}
		return []StyledSpan{{Text: text}}
	}
	spans := []StyledSpan{}
	var buf strings.Builder
	style := CellStyle{}
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		spans = append(spans, StyledSpan{Text: buf.String(), Style: style})
		buf.Reset()
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b {
			if i+1 < len(runes) && runes[i+1] == '[' {
				j := i + 2
				for j < len(runes) && !isCSIFinal(runes[j]) {
					j++
				}
				if j >= len(runes) {
					break
				}
				if runes[j] == 'm' {
					flush()
					style = applySGR(style, string(runes[i+2:j]))
				}
				i = j
				continue
			}
			// Other escape forms (for example an OSC introducer or a two-byte
			// ESC sequence): drop the introducer and one following byte.
			if i+1 < len(runes) {
				i++
			}
			continue
		}
		buf.WriteRune(r)
	}
	flush()
	return spans
}

func isCSIFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7e
}

func applySGR(style CellStyle, params string) CellStyle {
	if params == "" {
		return CellStyle{}
	}
	nums := make([]int, 0, 4)
	for _, part := range strings.Split(params, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			nums = append(nums, 0)
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return style
		}
		nums = append(nums, n)
	}
	for i := 0; i < len(nums); i++ {
		n := nums[i]
		switch {
		case n == 0:
			style = CellStyle{}
		case n == 1:
			style.Bold = true
		case n == 2:
			style.Dim = true
		case n == 3:
			style.Italic = true
		case n == 4:
			style.Underline = true
		case n == 22:
			style.Bold = false
			style.Dim = false
		case n == 23:
			style.Italic = false
		case n == 24:
			style.Underline = false
		case n >= 30 && n <= 37:
			style.Foreground = strconv.Itoa(n - 30)
		case n == 38 || n == 48:
			value, consumed, ok := extendedColor(nums[i+1:])
			if !ok {
				continue
			}
			if n == 38 {
				style.Foreground = value
			} else {
				style.Background = value
			}
			i += consumed
		case n == 39:
			style.Foreground = ""
		case n >= 40 && n <= 47:
			style.Background = strconv.Itoa(n - 40)
		case n == 49:
			style.Background = ""
		case n >= 90 && n <= 97:
			style.Foreground = strconv.Itoa(n - 80)
		case n >= 100 && n <= 107:
			style.Background = strconv.Itoa(n - 100)
		}
	}
	return style
}

// extendedColor parses the tail of a 38/48 parameter list. It returns the
// lipgloss-compatible colour string and how many parameters it consumed.
func extendedColor(rest []int) (string, int, bool) {
	if len(rest) == 0 {
		return "", 0, false
	}
	switch rest[0] {
	case 5:
		if len(rest) < 2 {
			return "", 0, false
		}
		return strconv.Itoa(rest[1]), 2, true
	case 2:
		if len(rest) < 4 {
			return "", 0, false
		}
		return fmt.Sprintf("#%02x%02x%02x", clampColor(rest[1]), clampColor(rest[2]), clampColor(rest[3])), 4, true
	default:
		return "", 0, false
	}
}

func clampColor(value int) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return value
}

// PlainTextFromANSI returns text with ANSI escape sequences removed.
func PlainTextFromANSI(text string) string {
	if !strings.ContainsRune(text, 0x1b) {
		return text
	}
	var builder strings.Builder
	for _, span := range ParseANSISpans(text) {
		builder.WriteString(span.Text)
	}
	return builder.String()
}

// SpanText flattens styled spans to text.
func SpanText(spans []StyledSpan) string {
	var builder strings.Builder
	for _, span := range spans {
		builder.WriteString(span.Text)
	}
	return builder.String()
}

// StyledLinesFromSpans splits styled spans at newlines without losing styles
// that started on an earlier line.
func StyledLinesFromSpans(spans []StyledSpan) []StyledLine {
	out := []StyledLine{}
	current := StyledLine{}
	flush := func() {
		out = append(out, current)
		current = StyledLine{}
	}
	for _, span := range spans {
		text := span.Text
		for {
			index := strings.IndexByte(text, '\n')
			if index < 0 {
				if text != "" {
					current = append(current, StyledSpan{Text: text, Style: span.Style})
				}
				break
			}
			if index > 0 {
				current = append(current, StyledSpan{Text: text[:index], Style: span.Style})
			}
			flush()
			text = text[index+1:]
		}
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out
}

type styledRune struct {
	r     rune
	style CellStyle
}

// styledRunesFromSpans lists the non-space runes of the spans with their styles,
// in order. Wrapping drops whitespace and re-inserts its own, so only non-space
// runes are needed to re-attach styles to wrapped output.
func styledRunesFromSpans(spans []StyledSpan) []styledRune {
	runes := []styledRune{}
	for _, span := range spans {
		for _, r := range span.Text {
			if unicode.IsSpace(r) {
				continue
			}
			runes = append(runes, styledRune{r: r, style: span.Style})
		}
	}
	return runes
}

func spansHaveStyle(spans []StyledSpan) bool {
	for _, span := range spans {
		if span.Style != (CellStyle{}) {
			return true
		}
	}
	return false
}

// WrapStyledLine wraps text that may carry ANSI styles. The plain text of the
// result is identical to tui.AdaptiveWrapLine on the escape-stripped text, and
// each source rune keeps its parsed style.
func WrapStyledLine(text string, options tui.WrapOptions) []StyledLine {
	if !strings.ContainsRune(text, 0x1b) {
		return StyledLinesFromPlain(tui.AdaptiveWrapLine(text, options))
	}
	return WrapStyledSpans(ParseANSISpans(text), options)
}

// WrapStyledSpans wraps already parsed styled spans.
func WrapStyledSpans(spans []StyledSpan, options tui.WrapOptions) []StyledLine {
	plain := SpanText(spans)
	if !spansHaveStyle(spans) {
		return StyledLinesFromPlain(tui.AdaptiveWrapLine(plain, options))
	}
	return wrapStyledRunes(plain, styledRunesFromSpans(spans), options)
}

// wrapStyledRunes wraps plain text while re-attaching per-rune styles. Wrapping
// only ever inserts whitespace plus the caller-supplied indent prefixes and
// keeps every non-space rune in source order, so styles can be re-attached
// positionally once the indent prefix is removed.
func wrapStyledRunes(plain string, source []styledRune, options tui.WrapOptions) []StyledLine {
	wrapped := tui.AdaptiveWrapLine(plain, options)
	out := make([]StyledLine, 0, len(wrapped))
	index := 0
	for position, line := range wrapped {
		indent := options.InitialIndent
		if position > 0 {
			indent = options.SubsequentIndent
		}
		body := line
		if indent != "" && strings.HasPrefix(body, indent) {
			body = body[len(indent):]
		} else {
			indent = ""
		}
		styled := StyledLine{}
		if indent != "" {
			styled = append(styled, StyledSpan{Text: indent})
		}
		styledBody, next := styleWrappedBody(body, source, index)
		index = next
		out = append(out, append(styled, styledBody...))
	}
	return out
}

// styleWrappedBody attaches styles to one wrapped line body (indent removed)
// and returns the index of the next unconsumed source rune.
func styleWrappedBody(body string, source []styledRune, index int) (StyledLine, int) {
	spans := []StyledSpan{}
	var buf strings.Builder
	current := CellStyle{}
	started := false
	flush := func() {
		if !started {
			return
		}
		spans = append(spans, StyledSpan{Text: buf.String(), Style: current})
		buf.Reset()
		started = false
	}
	for _, r := range body {
		if !unicode.IsSpace(r) && index < len(source) {
			style := source[index].style
			if !started || style != current {
				flush()
				current = style
				started = true
			}
			index++
			buf.WriteRune(r)
			continue
		}
		buf.WriteRune(r)
		started = true
	}
	flush()
	return StyledLine(spans), index
}
