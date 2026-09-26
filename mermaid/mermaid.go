package mermaid

import (
	"strings"
	"unicode"
)

// Rust parity: codex-rs/mermaid/src/lib.rs's bounds.
const (
	maxSource = 16 * 1024
	maxNodes  = 16
	maxEdges  = 24
	maxLabel  = 40
	maxCells  = 64 * 1024
)

// RenderError is a diagram this bounded prototype cannot faithfully represent
// (Rust `RenderError`). The values are comparable so callers can branch on them.
type RenderError string

const (
	// ErrUnsupported marks syntax or text outside the explicitly supported subset.
	ErrUnsupported RenderError = "unsupported Mermaid syntax or label"
	// ErrLimit marks a source, diagram, label or canvas limit being exceeded.
	ErrLimit RenderError = "diagram exceeds prototype limits"
	// ErrTooWide marks a complete diagram exceeding the supplied display width.
	ErrTooWide RenderError = "diagram exceeds available display width"
)

func (e RenderError) Error() string { return string(e) }

// Render draws a bounded subset of Mermaid as plain Unicode text.
//
// Unknown syntax, unsafe terminal text and diagrams exceeding maxWidth return
// errors; no partial result is returned.
func Render(source string, maxWidth int) (string, error) {
	spans, err := RenderSpans(source, maxWidth)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(spans))
	for _, line := range spans {
		var builder strings.Builder
		for _, span := range line {
			builder.WriteString(span.Text)
		}
		lines = append(lines, builder.String())
	}
	return strings.Join(lines, "\n"), nil
}

// RenderSpans renders the same bounded diagram as lines of semantic spans for
// caller-provided styling.
func RenderSpans(source string, maxWidth int) ([][]Span, error) {
	if len(source) > maxSource {
		return nil, ErrLimit
	}
	lines := splitSourceLines(source)
	statements := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "%%") {
			continue
		}
		for _, part := range strings.Split(line, ";") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				statements = append(statements, trimmed)
			}
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "%%{") {
			return nil, ErrUnsupported
		}
	}
	if len(statements) == 0 {
		return nil, ErrUnsupported
	}
	header := statements[0]
	body := statements[1:]
	switch header {
	case "sequenceDiagram":
		return renderSequence(body, maxWidth)
	case "stateDiagram-v2", "stateDiagram", "classDiagram", "erDiagram":
		graph, err := parseRelations(header, body)
		if err != nil {
			return nil, err
		}
		return renderGraph(graph, maxWidth)
	default:
		graph, err := parseFlowchart(header, body)
		if err != nil {
			return nil, err
		}
		return renderGraph(graph, maxWidth)
	}
}

// splitSourceLines mirrors Rust's `str::lines`: split on '\n' with a trailing
// '\r' removed.
func splitSourceLines(source string) []string {
	raw := strings.Split(source, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, strings.TrimSuffix(line, "\r"))
	}
	return lines
}

type direction int

const (
	directionDown direction = iota
	directionUp
	directionRight
	directionLeft
)

func parseDirection(text string) (direction, error) {
	switch text {
	case "TD", "TB":
		return directionDown, nil
	case "BT":
		return directionUp, nil
	case "LR":
		return directionRight, nil
	case "RL":
		return directionLeft, nil
	default:
		return 0, ErrUnsupported
	}
}

// shape names the node shapes the terminal renderer supports.
type shape int

const (
	shapeRectangle shape = iota
	shapeDecision
	shapeStadium
)

type node struct {
	id       string
	label    string
	shape    shape
	declared bool
	members  []string
}

type edge struct {
	from        int
	to          int
	label       string
	targetLabel string
	sourceTip   rune
	targetTip   rune
	dashed      bool
}

// directedEdge mirrors `Edge::directed`: a solid edge with a filled arrow head.
func directedEdge(from, to int, label string) edge {
	return edge{from: from, to: to, label: label, sourceTip: '\u2500', targetTip: '\u25c4'}
}

type graph struct {
	direction direction
	nodes     []node
	edges     []edge
}

// node returns the index of an existing node or appends a new undeclared one,
// enforcing the prototype's node limit.
func (g *graph) node(id string) (int, error) {
	for index := range g.nodes {
		if g.nodes[index].id == id {
			return index, nil
		}
	}
	if len(g.nodes) == maxNodes {
		return 0, ErrLimit
	}
	g.nodes = append(g.nodes, node{id: id, label: id, shape: shapeRectangle})
	return len(g.nodes) - 1, nil
}
