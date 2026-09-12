package chatwidget

// Styled split-flap rendering. The plain AnimateLines path stays the frozen
// text contract; AnimateStyledLines mirrors it and additionally assigns the
// Rust board colours (black board, grey glyphs, dark grey while flipping, and a
// cyan/magenta afterglow for the settled speaker). A test asserts the two paths
// produce identical text so they cannot drift.

import (
	"strings"
	"time"

	historycell "codex_go/tui/history_cell"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Ratatui colour equivalents used by the Rust split-flap board.
const (
	flapColorBlack    = "0"
	flapColorGray     = "7"
	flapColorDarkGray = "8"
	flapColorMagenta  = "13"
	flapColorCyan     = "14"
)

func splitFlapBoardStyle() historycell.CellStyle {
	return historycell.CellStyle{Background: flapColorBlack, Foreground: flapColorGray}
}

// AnimateStyledLines renders the animated lines with per-span colour.
func (b *SplitFlapBoard) AnimateStyledLines(lines []string, width int, elapsed time.Duration) []historycell.StyledLine {
	out := make([]historycell.StyledLine, 0, len(lines))
	if b == nil || !b.Animated {
		return historycell.StyledLinesFromPlain(lines)
	}
	now := b.StartedAt.Add(elapsed)
	phaseElapsed := now.Sub(b.PhaseStartedAt)
	glyphIndex := 0
	wordUnsettled := false
	finalLine := -1
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			finalLine = index
		}
	}
	for lineIndex, line := range lines {
		if strings.TrimSpace(line) == "" {
			out = append(out, historycell.StyledLine{{Text: line}})
			continue
		}
		board := splitFlapBoardStyle()
		spans := []historycell.StyledSpan{}
		appendSpan := func(text string, style historycell.CellStyle) {
			if text == "" {
				return
			}
			if len(spans) > 0 && spans[len(spans)-1].Style == style {
				spans[len(spans)-1].Text += text
				return
			}
			spans = append(spans, historycell.StyledSpan{Text: text, Style: style})
		}
		graphemes := uniseg.NewGraphemes(line)
		for graphemes.Next() {
			grapheme := graphemes.Str()
			if splitFlapIsFlippable(grapheme) {
				position := glyphIndex
				glyphIndex++
				arrival := b.StartedAt
				if position < len(b.TileArrivals) {
					arrival = b.TileArrivals[position]
				}
				tileElapsed := now.Sub(arrival)
				text, flipping := splitFlapGlyph(grapheme, position, tileElapsed, phaseElapsed, b.FlapSample)
				style := board
				switch {
				case flipping:
					wordUnsettled = true
					style.Foreground = flapColorDarkGray
				case tileElapsed < SplitFlapTileSettleDuration+SplitFlapTileAfterglowDuration:
					if b.Role == "user" {
						style.Foreground = flapColorCyan
					} else {
						style.Foreground = flapColorMagenta
					}
				}
				appendSpan(text, style)
				continue
			}
			if strings.TrimSpace(grapheme) == "" {
				wordUnsettled = false
			}
			style := board
			switch grapheme {
			case "\u203a":
				style.Foreground = flapColorCyan
			case "\u2022":
				style.Foreground = flapColorMagenta
			}
			if wordUnsettled && splitFlapIsPunctuation(grapheme) {
				appendSpan(" ", style)
			} else {
				appendSpan(grapheme, style)
			}
		}
		text := joinStyledText(spans)
		remaining := width - runewidth.StringWidth(text)
		if lineIndex == finalLine && b.IsAnimating(elapsed) && remaining > 1 && len(b.FlapSample) > 0 &&
			len(b.TileArrivals) > 0 && now.Sub(b.TileArrivals[0]) >= SplitFlapTileSettleDuration {
			phase := int(phaseElapsed / SplitFlapFrameInterval)
			count := min(remaining-1, 3)
			runway := make([]byte, 0, count+1)
			runway = append(runway, ' ')
			for index := 0; index < count; index++ {
				runway = append(runway, b.FlapSample[(phase+index)%len(b.FlapSample)])
			}
			style := board
			style.Foreground = flapColorDarkGray
			appendSpan(string(runway), style)
			text += string(runway)
		}
		if padding := width - runewidth.StringWidth(text); padding > 0 {
			appendSpan(strings.Repeat(" ", padding), board)
		}
		out = append(out, historycell.StyledLine(spans))
	}
	return out
}

func joinStyledText(spans []historycell.StyledSpan) string {
	var builder strings.Builder
	for _, span := range spans {
		builder.WriteString(span.Text)
	}
	return builder.String()
}
