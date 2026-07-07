package historycell

import (
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/base.rs.

type HistoryCell interface {
	DisplayLines(width int) []string
	RawLines() []string
}

type HyperlinkLine struct {
	Text  string
	Links []tui.TerminalHyperlink
}

type PlainHistoryCell struct {
	Lines []string
}

func NewPlainHistoryCell(lines []string) PlainHistoryCell {
	return PlainHistoryCell{Lines: append([]string(nil), lines...)}
}

func (c PlainHistoryCell) DisplayLines(width int) []string {
	_ = width
	return append([]string(nil), c.Lines...)
}

func (c PlainHistoryCell) RawLines() []string {
	return plainLines(c.Lines)
}

type WebHyperlinkHistoryCell struct {
	Lines []string
}

func NewWebHyperlinkHistoryCell(lines []string) WebHyperlinkHistoryCell {
	return WebHyperlinkHistoryCell{Lines: append([]string(nil), lines...)}
}

func (c WebHyperlinkHistoryCell) DisplayLines(width int) []string {
	_ = width
	return append([]string(nil), c.Lines...)
}

func (c WebHyperlinkHistoryCell) DisplayHyperlinkLines(width int) []HyperlinkLine {
	_ = width
	out := make([]HyperlinkLine, 0, len(c.Lines))
	for _, line := range c.Lines {
		out = append(out, HyperlinkLine{Text: line, Links: tui.WebLinksInText(line)})
	}
	return out
}

func (c WebHyperlinkHistoryCell) RawLines() []string {
	return plainLines(c.Lines)
}

type PrefixedWrappedHistoryCell struct {
	Text             string
	InitialPrefix    string
	SubsequentPrefix string
}

func NewPrefixedWrappedHistoryCell(text string, initialPrefix string, subsequentPrefix string) PrefixedWrappedHistoryCell {
	return PrefixedWrappedHistoryCell{
		Text:             text,
		InitialPrefix:    initialPrefix,
		SubsequentPrefix: subsequentPrefix,
	}
}

func (c PrefixedWrappedHistoryCell) DisplayLines(width int) []string {
	if width <= 0 {
		return nil
	}
	return tui.WrapLines(strings.Split(c.Text, "\n"), tui.WrapOptions{
		Width:            width,
		InitialIndent:    c.InitialPrefix,
		SubsequentIndent: c.SubsequentPrefix,
		BreakWords:       true,
	})
}

func (c PrefixedWrappedHistoryCell) RawLines() []string {
	return rawLinesFromSource(c.Text)
}

type CompositeHistoryCell struct {
	Parts []HistoryCell
}

func NewCompositeHistoryCell(parts []HistoryCell) CompositeHistoryCell {
	return CompositeHistoryCell{Parts: append([]HistoryCell(nil), parts...)}
}

func (c CompositeHistoryCell) DisplayLines(width int) []string {
	return joinCellLines(c.Parts, width, false)
}

func (c CompositeHistoryCell) RawLines() []string {
	out := []string{}
	first := true
	for _, part := range c.Parts {
		lines := part.RawLines()
		if len(lines) == 0 {
			continue
		}
		if !first {
			out = append(out, "")
		}
		out = append(out, lines...)
		first = false
	}
	return out
}

func joinCellLines(parts []HistoryCell, width int, raw bool) []string {
	out := []string{}
	first := true
	for _, part := range parts {
		var lines []string
		if raw {
			lines = part.RawLines()
		} else {
			lines = part.DisplayLines(width)
		}
		if len(lines) == 0 {
			continue
		}
		if !first {
			out = append(out, "")
		}
		out = append(out, lines...)
		first = false
	}
	return out
}

func rawLinesFromSource(source string) []string {
	source = strings.TrimRight(source, "\r\n")
	if source == "" {
		return nil
	}
	return strings.Split(source, "\n")
}

func plainLines(lines []string) []string {
	return append([]string(nil), lines...)
}
