package tui

import "github.com/rivo/uniseg"

// Rust parity: codex-rs/tui/src/live_wrap.rs.

type Row struct {
	Text          string
	ExplicitBreak bool
}

func (r Row) Width() int {
	return DisplayWidth(r.Text)
}

type RowBuilder struct {
	targetWidth int
	currentLine string
	rows        []Row
}

func NewRowBuilder(targetWidth int) *RowBuilder {
	if targetWidth < 1 {
		targetWidth = 1
	}
	return &RowBuilder{targetWidth: targetWidth}
}

func (b *RowBuilder) Width() int {
	return b.targetWidth
}

func (b *RowBuilder) SetWidth(width int) {
	if width < 1 {
		width = 1
	}
	if width == b.targetWidth {
		return
	}
	b.targetWidth = width
	all := ""
	for _, row := range b.rows {
		all += row.Text
		if row.ExplicitBreak {
			all += "\n"
		}
	}
	all += b.currentLine
	b.rows = nil
	b.currentLine = ""
	b.PushFragment(all)
}

func (b *RowBuilder) PushFragment(fragment string) {
	if fragment == "" {
		return
	}
	start := 0
	for i, r := range fragment {
		if r != '\n' {
			continue
		}
		if start < i {
			b.currentLine += fragment[start:i]
		}
		b.flushCurrentLine(true)
		start = i + len(string(r))
	}
	if start < len(fragment) {
		b.currentLine += fragment[start:]
		b.wrapCurrentLine()
	}
}

func (b *RowBuilder) EndLine() {
	b.flushCurrentLine(true)
}

func (b *RowBuilder) Rows() []Row {
	return append([]Row(nil), b.rows...)
}

func (b *RowBuilder) DisplayRows() []Row {
	out := b.Rows()
	if b.currentLine != "" {
		out = append(out, Row{Text: b.currentLine})
	}
	return out
}

func (b *RowBuilder) DrainCommitReady(maxKeep int) []Row {
	displayCount := len(b.rows)
	if b.currentLine != "" {
		displayCount++
	}
	if displayCount <= maxKeep {
		return nil
	}
	toCommit := displayCount - maxKeep
	if toCommit > len(b.rows) {
		toCommit = len(b.rows)
	}
	drained := append([]Row(nil), b.rows[:toCommit]...)
	b.rows = append([]Row(nil), b.rows[toCommit:]...)
	return drained
}

func (b *RowBuilder) flushCurrentLine(explicitBreak bool) {
	b.wrapCurrentLine()
	if explicitBreak {
		if b.currentLine == "" {
			b.rows = append(b.rows, Row{ExplicitBreak: true})
		} else {
			b.rows = append(b.rows, Row{Text: b.currentLine, ExplicitBreak: true})
		}
	}
	b.currentLine = ""
}

func (b *RowBuilder) wrapCurrentLine() {
	for b.currentLine != "" {
		prefix, suffix, taken := TakePrefixByWidth(b.currentLine, b.targetWidth)
		if taken == 0 {
			graphemes := uniseg.NewGraphemes(b.currentLine)
			if !graphemes.Next() {
				return
			}
			_, end := graphemes.Positions()
			b.rows = append(b.rows, Row{Text: b.currentLine[:end]})
			b.currentLine = b.currentLine[end:]
			continue
		}
		if suffix == "" {
			return
		}
		b.rows = append(b.rows, Row{Text: prefix})
		b.currentLine = suffix
	}
}

func TakePrefixByWidth(text string, maxCols int) (string, string, int) {
	if maxCols <= 0 || text == "" {
		return "", text, 0
	}
	cols := 0
	end := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		_, graphemeEnd := graphemes.Positions()
		width := DisplayWidth(grapheme)
		if cols+width > maxCols {
			break
		}
		cols += width
		end = graphemeEnd
		if cols == maxCols {
			break
		}
	}
	return text[:end], text[end:], cols
}
