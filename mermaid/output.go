// Package mermaid renders a bounded subset of Mermaid diagrams as plain
// Unicode text. It is a Go port of codex-rs/mermaid.
//
// The renderer supports flowcharts, sequence diagrams, flat state diagrams,
// class diagrams and ER diagrams. Unsafe terminal text, unsupported syntax and
// diagrams exceeding the caller's width return errors; no partial result is
// returned, leaving source fallback to the caller. The package performs no I/O.
package mermaid

import (
	"strings"
	"unicode"
)

// Role is the diagram element a caller can style with its own theme
// (Rust `output::Role`).
type Role string

const (
	RoleNode Role = "node"
	RoleEdge Role = "edge"
	RoleText Role = "text"
)

// Span is adjacent characters with one semantic role, independent of terminal
// styling (Rust `output::Span`).
type Span struct {
	Text string
	Role Role
}

// cell is one drawing cell; drawing cells never contain ANSI escapes.
type cell struct {
	symbol rune
	role   Role
}

func edgeCell(symbol rune) cell { return cell{symbol: symbol, role: RoleEdge} }
func nodeCell(symbol rune) cell { return cell{symbol: symbol, role: RoleNode} }

// finish merges each row's cells into role runs, skipping the wide-character
// placeholders and trimming trailing whitespace.
func finish(rows [][]cell) [][]Span {
	out := make([][]Span, 0, len(rows))
	for _, row := range rows {
		spans := make([]Span, 0, len(row))
		for _, current := range row {
			if current.symbol == 0 {
				continue
			}
			if len(spans) > 0 && spans[len(spans)-1].Role == current.role {
				spans[len(spans)-1].Text += string(current.symbol)
				continue
			}
			spans = append(spans, Span{Text: string(current.symbol), Role: current.role})
		}
		for len(spans) > 0 {
			last := &spans[len(spans)-1]
			last.Text = strings.TrimRightFunc(last.Text, unicode.IsSpace)
			if last.Text != "" {
				break
			}
			spans = spans[:len(spans)-1]
		}
		out = append(out, spans)
	}
	return out
}
