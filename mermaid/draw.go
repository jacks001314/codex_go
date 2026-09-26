package mermaid

import "strings"

// renderGraph lays out orthogonal routes with unique lanes and endpoint
// positions in all four directions (Rust `draw::render`). Routes never share
// segments; crossings are explicitly marked and labels occupy reserved gutters.
func renderGraph(g *graph, maxWidth int) ([][]Span, error) {
	horizontal := g.direction == directionRight || g.direction == directionLeft
	labels := make([][]string, len(g.nodes))
	for i, n := range g.nodes {
		first := n.label
		if n.shape == shapeDecision {
			first = "\u25c7 " + n.label
		}
		lines := []string{first}
		if len(n.members) > 0 {
			lines = append(lines, strings.Repeat("\u2500", textWidth(n.label)))
			lines = append(lines, n.members...)
		}
		labels[i] = lines
	}
	labelWidth := 0
	for _, e := range g.edges {
		if w := textWidth(e.label); w > labelWidth {
			labelWidth = w
		}
		if w := textWidth(e.targetLabel); w > labelWidth {
			labelWidth = w
		}
	}
	var boxCross int
	if horizontal {
		maxLines := 0
		for _, lines := range labels {
			if len(lines) > maxLines {
				maxLines = len(lines)
			}
		}
		boxCross = maxLines + 2
	} else {
		maxW := 0
		for _, lines := range labels {
			for _, line := range lines {
				if w := textWidth(line); w > maxW {
					maxW = w
				}
			}
		}
		boxCross = maxW + 4
	}
	counts := make([]int, len(g.nodes))
	ports := make([][2]int, len(g.edges))
	for i, e := range g.edges {
		source := counts[e.from]
		counts[e.from]++
		target := counts[e.to]
		counts[e.to]++
		ports[i] = [2]int{source, target}
	}
	stride := 1
	if horizontal {
		stride = labelWidth + 2
	}
	sizes := make([]int, len(g.nodes))
	for i, lines := range labels {
		if horizontal {
			maxW := 0
			for _, line := range lines {
				if w := textWidth(line); w > maxW {
					maxW = w
				}
			}
			size := maxW + 4
			if alternative := counts[i]*stride + 2; alternative > size {
				size = alternative
			}
			sizes[i] = size
		} else {
			sizes[i] = len(lines) + counts[i] + 2
		}
	}
	starts := make([]int, len(g.nodes))
	along := 0
	for i := 0; i < len(g.nodes); i++ {
		index := i
		if g.direction == directionUp || g.direction == directionLeft {
			index = len(g.nodes) - 1 - i
		}
		starts[index] = along
		along += sizes[index] + 2
	}
	firstLane := boxCross
	if horizontal {
		firstLane += 4
	} else {
		firstLane += labelWidth + 5
	}
	across := boxCross
	if len(g.edges) > 0 {
		across = firstLane + len(g.edges)*2 - 1
	}
	width, height := across, along-2
	if horizontal {
		width, height = along-2, across
	}
	if width > maxWidth {
		return nil, ErrTooWide
	}
	if width*height > maxCells {
		return nil, ErrLimit
	}
	canvas := newCanvas(width, height, horizontal)
	for i, lines := range labels {
		start := starts[i]
		end := start + sizes[i] - 1
		topLeft, topRight, bottomLeft, bottomRight := '\u250c', '\u2510', '\u2514', '\u2518'
		if g.nodes[i].shape == shapeStadium {
			topLeft, topRight, bottomLeft, bottomRight = '\u256d', '\u256e', '\u2570', '\u256f'
		}
		canvas.set(0, start, nodeCell(topLeft))
		canvas.set(boxCross-1, start, nodeCell(topRight))
		canvas.set(0, end, nodeCell(bottomLeft))
		canvas.set(boxCross-1, end, nodeCell(bottomRight))
		for x := 1; x < boxCross-1; x++ {
			canvas.set(x, start, nodeCell('\u2500'))
			canvas.set(x, end, nodeCell('\u2500'))
		}
		for y := start + 1; y < end; y++ {
			canvas.set(0, y, nodeCell('\u2502'))
			canvas.set(boxCross-1, y, nodeCell('\u2502'))
		}
		for j, line := range lines {
			x, y := 2, start+j+1
			if horizontal {
				x, y = start+2, j+1
			}
			if err := putText(canvas.row(y), x, line); err != nil {
				return nil, err
			}
		}
	}
	endpoints := make([][2]int, len(g.edges))
	for i, e := range g.edges {
		offset := func(nodeIndex, port int) int {
			if horizontal {
				return starts[nodeIndex] + 1 + port*stride
			}
			return starts[nodeIndex] + len(labels[nodeIndex]) + 1 + port
		}
		endpoints[i] = [2]int{offset(e.from, ports[i][0]), offset(e.to, ports[i][1])}
	}
	// Paint lanes first so every crossing is independent of iteration order.
	for i, e := range g.edges {
		source, target := endpoints[i][0], endpoints[i][1]
		symbol := '\u2502'
		if e.dashed {
			symbol = '\u2506'
		}
		for y := minInt(source, target) + 1; y < maxInt(source, target); y++ {
			canvas.set(firstLane+2*i, y, edgeCell(symbol))
		}
	}
	for i, e := range g.edges {
		source, target := endpoints[i][0], endpoints[i][1]
		lane := firstLane + 2*i
		for _, y := range []int{source, target} {
			for x := boxCross; x < lane; x++ {
				ch := '\u2500'
				switch canvas.get(x, y) {
				case '\u2502', '\u2506':
					ch = '\u256a'
				default:
					if e.dashed {
						ch = '\u2504'
					}
				}
				canvas.set(x, y, edgeCell(ch))
			}
		}
		canvas.set(boxCross-1, source, nodeCell('\u251c'))
		canvas.set(boxCross-1, target, nodeCell('\u251c'))
		canvas.set(boxCross, source, edgeCell(e.sourceTip))
		canvas.set(boxCross, target, edgeCell(e.targetTip))
		if source < target {
			canvas.set(lane, source, edgeCell('\u2510'))
			canvas.set(lane, target, edgeCell('\u2518'))
		} else {
			canvas.set(lane, source, edgeCell('\u2518'))
			canvas.set(lane, target, edgeCell('\u2510'))
		}
		for _, labelled := range []struct {
			port  int
			label string
		}{{source, e.label}, {target, e.targetLabel}} {
			x, y := boxCross+2, labelled.port
			if horizontal {
				x, y = labelled.port+1, boxCross+1
			}
			if err := putText(canvas.row(y), x, labelled.label); err != nil {
				return nil, err
			}
		}
	}
	return finish(canvas.cells), nil
}

// canvas stores cells in the orientation the diagram's direction uses; the
// accessors transpose as needed.
type canvas struct {
	cells      [][]cell
	horizontal bool
}

func newCanvas(width, height int, horizontal bool) *canvas {
	cells := make([][]cell, height)
	for y := range cells {
		row := make([]cell, width)
		for x := range row {
			row[x] = edgeCell(' ')
		}
		cells[y] = row
	}
	return &canvas{cells: cells, horizontal: horizontal}
}

func (c *canvas) row(along int) []cell { return c.cells[along] }

func (c *canvas) set(across, along int, current cell) {
	if c.horizontal {
		current.symbol = transpose(current.symbol)
		c.cells[across][along] = current
		return
	}
	c.cells[along][across] = current
}

func (c *canvas) get(across, along int) rune {
	if c.horizontal {
		return transpose(c.cells[across][along].symbol)
	}
	return c.cells[along][across].symbol
}

func transpose(ch rune) rune {
	switch ch {
	case '\u2500':
		return '\u2502'
	case '\u2502':
		return '\u2500'
	case '\u2504':
		return '\u2506'
	case '\u2506':
		return '\u2504'
	case '\u2510':
		return '\u2514'
	case '\u2514':
		return '\u2510'
	case '\u256e':
		return '\u2570'
	case '\u2570':
		return '\u256e'
	case '\u251c':
		return '\u252c'
	case '\u252c':
		return '\u251c'
	case '\u25c4':
		return '\u25b2'
	case '\u25b2':
		return '\u25c4'
	case '\u25c1':
		return '\u25b3'
	case '\u25b3':
		return '\u25c1'
	default:
		return ch
	}
}

// putText writes a text run into a row, reserving the extra cells a wide scalar
// occupies.
func putText(row []cell, column int, text string) error {
	for _, ch := range text {
		width := charWidth(ch)
		if width <= 0 {
			return ErrUnsupported
		}
		if column >= len(row) {
			return ErrUnsupported
		}
		row[column] = cell{symbol: ch, role: RoleText}
		for offset := column + 1; offset < column+width && offset < len(row); offset++ {
			row[offset] = cell{role: RoleText}
		}
		column += width
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
