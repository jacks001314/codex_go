package mermaid

import (
	"sort"
	"strings"
	"testing"
)

// Mirrors Rust #45817's `reconstruct_every_edge_from_rendered_paths`: every one
// of the 512 directed three-node graphs, in all four layout directions, must
// keep every edge reconstructible from the emitted glyphs alone, and the node
// order must follow the layout direction.
func TestReconstructEveryEdgeFromRenderedPathsLikeRust(t *testing.T) {
	for mask := 0; mask < 512; mask++ {
		for _, direction := range []string{"TD", "BT", "LR", "RL"} {
			var source strings.Builder
			source.WriteString("graph " + direction + "; A; B; C;")
			expected := make([][2]rune, 0, 9)
			for from := 0; from < 3; from++ {
				for to := 0; to < 3; to++ {
					if mask&(1<<(from*3+to)) == 0 {
						continue
					}
					a := rune('A' + from)
					b := rune('A' + to)
					source.WriteString(string(a) + "-->" + string(b) + ";")
					expected = append(expected, [2]rune{a, b})
				}
			}
			text := source.String()
			output, err := Render(text, 100)
			if err != nil {
				t.Fatalf("Render(%q) error = %v", text, err)
			}
			rows := make([][]rune, 0, 16)
			for _, line := range strings.Split(output, "\n") {
				rows = append(rows, []rune(line))
			}
			horizontal := direction == "LR" || direction == "RL"
			if horizontal {
				width := 0
				for _, row := range rows {
					if len(row) > width {
						width = len(row)
					}
				}
				transposed := make([][]rune, 0, width)
				for x := 0; x < width; x++ {
					column := make([]rune, 0, len(rows))
					for _, row := range rows {
						cell := ' '
						if x < len(row) {
							cell = row[x]
						}
						column = append(column, transposeGlyph(cell))
					}
					transposed = append(transposed, column)
				}
				rows = transposed
			}
			owners := make([]rune, len(rows))
			order := make([]rune, 0, 3)
			for top, row := range rows {
				if len(row) == 0 || row[0] != '\u250c' {
					continue
				}
				bottom := -1
				for y := top + 1; y < len(rows); y++ {
					if len(rows[y]) > 0 && rows[y][0] == '\u2514' {
						bottom = y
						break
					}
				}
				if bottom < 0 {
					t.Fatalf("node box opened at row %d has no bottom border: %q", top, text)
				}
				owner := rune(0)
				for y := top; y <= bottom && owner == 0; y++ {
					for _, cell := range rows[y] {
						if cell == 'A' || cell == 'B' || cell == 'C' {
							owner = cell
							break
						}
					}
				}
				if owner == 0 {
					t.Fatalf("node box at row %d carries no label: %q", top, text)
				}
				for y := top; y <= bottom; y++ {
					owners[y] = owner
				}
				order = append(order, owner)
			}
			wantOrder := []rune{'A', 'B', 'C'}
			if direction == "BT" || direction == "RL" {
				wantOrder = []rune{'C', 'B', 'A'}
			}
			if len(order) != 3 || order[0] != wantOrder[0] || order[1] != wantOrder[1] || order[2] != wantOrder[2] {
				t.Fatalf("%s node order = %q, want %q", text, string(order), string(wantOrder))
			}
			actual := make([][2]rune, 0, 9)
			for y, row := range rows {
				port := portPosition(row)
				if port < 0 {
					continue
				}
				lane := -1
				for x := port + 1; x < len(row); x++ {
					if row[x] == '\u2510' || row[x] == '\u2518' {
						lane = x
						break
					}
				}
				if lane < 0 {
					t.Fatalf("edge port at row %d has no lane: %q", y, text)
				}
				step := -1
				if row[lane] == '\u2510' {
					step = 1
				}
				target := y
				for {
					target += step
					cell := rows[target][lane]
					if cell == '\u2518' || cell == '\u2510' {
						break
					}
					if cell != '\u2502' && cell != '\u256a' {
						t.Fatalf("broken vertical path in %q at row %d: %q", text, target, cell)
					}
				}
				if target < 0 || target >= len(rows) || port+1 >= len(rows[target]) {
					t.Fatalf("edge path in %q leaves the canvas", text)
				}
				if rows[target][port] != '\u251c' || rows[target][port+1] != '\u25c4' {
					t.Fatalf("edge head in %q at row %d = %q, want the filled arrow", text, target, string(rows[target][port:port+2]))
				}
				for x := port + 2; x < lane; x++ {
					if rows[target][x] != '\u2500' && rows[target][x] != '\u256a' {
						t.Fatalf("edge lane in %q carries %q", text, rows[target][x])
					}
				}
				actual = append(actual, [2]rune{owners[y], owners[target]})
			}
			sortEdges(actual)
			sortEdges(expected)
			if len(actual) != len(expected) {
				t.Fatalf("%s reconstructed %v edges, want %v", text, actual, expected)
			}
			for i := range expected {
				if actual[i] != expected[i] {
					t.Fatalf("%s reconstructed edges = %v, want %v", text, actual, expected)
				}
			}
		}
	}
}

func transposeGlyph(cell rune) rune {
	switch cell {
	case '\u2500':
		return '\u2502'
	case '\u2502':
		return '\u2500'
	case '\u2510':
		return '\u2514'
	case '\u2514':
		return '\u2510'
	case '\u252c':
		return '\u251c'
	case '\u25b2':
		return '\u25c4'
	default:
		return cell
	}
}

func portPosition(row []rune) int {
	for i := 0; i+1 < len(row); i++ {
		if row[i] == '\u251c' && row[i+1] == '\u2500' {
			return i
		}
	}
	return -1
}

func sortEdges(edges [][2]rune) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i][0] != edges[j][0] {
			return edges[i][0] < edges[j][0]
		}
		return edges[i][1] < edges[j][1]
	})
}

// Mirrors Rust #45817's semantic-span check: a message label and an endpoint
// glyph that print the same character keep distinct roles, and node borders are
// reported as node spans.
func TestSemanticSpansDistinguishLabelsFromEndpointGlyphsLikeRust(t *testing.T) {
	lines, err := RenderSpans("sequenceDiagram; A-xB: x", 100)
	if err != nil {
		t.Fatalf("RenderSpans() error = %v", err)
	}
	roles := make([]Role, 0, 2)
	for _, line := range lines {
		for _, span := range line {
			for _, char := range span.Text {
				if char == 'x' {
					roles = append(roles, span.Role)
				}
			}
		}
	}
	if len(roles) != 2 || roles[0] != RoleText || roles[1] != RoleEdge {
		t.Fatalf("roles for the shared glyph = %v, want [text edge]", roles)
	}
	nodeSpan := false
	for _, span := range lines[0] {
		if strings.ContainsRune(span.Text, '\u250c') {
			if span.Role != RoleNode {
				t.Fatalf("box border role = %q, want node", span.Role)
			}
			nodeSpan = true
		}
	}
	if !nodeSpan {
		t.Fatalf("first line has no node border span: %#v", lines[0])
	}
}
