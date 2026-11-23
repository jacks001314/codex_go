package mermaid

import (
	"sort"
	"strings"
)

// renderGraph lays out orthogonal routes with unique lanes and endpoint
// positions in all four directions (Rust `draw::render`). Routes never share
// segments; crossings are explicitly marked and labels occupy reserved gutters.
func renderGraph(g *graph, maxWidth int) ([][]Span, error) {
	horizontal := g.direction == directionRight || g.direction == directionLeft
	labels := make([][]string, len(g.nodes))
	for i, n := range g.nodes {
		// Rust #54419: a node label may carry `<br>` line breaks. The box grows by
		// one rendered line per break, and the decision marker stays on the first
		// line only.
		lines := strings.Split(n.label, "\n")
		if n.shape == shapeDecision {
			lines[0] = "\u25c7 " + lines[0]
		}
		if len(n.members) > 0 {
			lines = append(lines, strings.Repeat("\u2500", textWidth(n.label)))
			lines = append(lines, n.members...)
		}
		labels[i] = lines
	}
	// Rust #62550: an edge label may carry `<br>` line breaks. The gutter is as
	// wide as the widest rendered line, and vertical ports reserve one row per
	// line so two-line labels do not overlap.
	labelWidth := 0
	labelHeight := 1
	for _, e := range g.edges {
		for _, label := range []string{e.label, e.targetLabel} {
			lines := strings.Split(label, "\n")
			if len(lines) > labelHeight {
				labelHeight = len(lines)
			}
			for _, line := range lines {
				if w := textWidth(line); w > labelWidth {
					labelWidth = w
				}
			}
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
		counts[e.from] += portRows(horizontal, e.label)
		target := counts[e.to]
		counts[e.to] += portRows(horizontal, e.targetLabel)
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
	grouped := len(g.subgraphs) > 0
	inset := 0
	if grouped {
		inset = 2
	}
	membership := make([]int, len(g.nodes))
	for i := range membership {
		membership[i] = -1
	}
	for group, flat := range g.subgraphs {
		for _, member := range flat.nodes {
			membership[member] = group
		}
	}
	// Keep a group's members contiguous even when an edge referenced them before
	// its declaration.
	order := make([]int, 0, len(g.nodes))
	placed := make([]bool, len(g.nodes))
	for n, group := range membership {
		if placed[n] {
			continue
		}
		if group < 0 {
			placed[n] = true
			order = append(order, n)
			continue
		}
		members := append([]int(nil), g.subgraphs[group].nodes...)
		sort.Ints(members)
		for _, member := range members {
			if placed[member] {
				continue
			}
			placed[member] = true
			order = append(order, member)
		}
	}
	if g.direction == directionUp || g.direction == directionLeft {
		for left, right := 0, len(order)-1; left < right; left, right = left+1, right-1 {
			order[left], order[right] = order[right], order[left]
		}
	}
	along := 0
	frames := make([][2]int, len(g.subgraphs))
	for position := 0; position < len(order); {
		index := order[position]
		group := membership[index]
		if group < 0 {
			starts[index] = along
			along += sizes[index] + 2
			position++
			continue
		}
		frameStart := along
		along += 2
		for position < len(order) && membership[order[position]] == group {
			member := order[position]
			starts[member] = along
			along += sizes[member] + 2
			position++
		}
		if horizontal {
			if extent := frameStart + textWidth(g.subgraphs[group].label) + 4; extent > along {
				along = extent
			}
		}
		frames[group] = [2]int{frameStart, along - 1}
		along += 2
	}
	firstLane := inset + boxCross
	if horizontal {
		firstLane += labelHeight + 3
	} else {
		firstLane += labelWidth + 5
	}
	internal := make([]bool, len(g.edges))
	internalCount := 0
	for i, e := range g.edges {
		if membership[e.from] >= 0 && membership[e.from] == membership[e.to] {
			internal[i] = true
			internalCount++
		}
	}
	frameCross := inset + boxCross + 2
	if len(g.edges) > 0 && firstLane+internalCount*2+1 > frameCross {
		frameCross = firstLane + internalCount*2 + 1
	}
	if !horizontal {
		for _, flat := range g.subgraphs {
			if groupWidth := textWidth(flat.label) + 4; groupWidth > frameCross {
				frameCross = groupWidth
			}
		}
	}
	externalLane := firstLane
	if grouped {
		externalLane = frameCross + 2
	}
	lanes := make([]int, len(g.edges))
	nextInternal, nextExternal := 0, 0
	for i := range g.edges {
		if internal[i] {
			lanes[i] = firstLane + nextInternal*2
			nextInternal++
			continue
		}
		lanes[i] = externalLane + nextExternal*2
		nextExternal++
	}
	across := inset + boxCross
	for _, lane := range lanes {
		if lane+1 > across {
			across = lane + 1
		}
	}
	if grouped && frameCross > across {
		across = frameCross
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
	for group, frame := range frames {
		start, end := frame[0], frame[1]
		for x := 1; x < frameCross-1; x++ {
			canvas.set(x, start, nodeCell('\u2500'))
			canvas.set(x, end, nodeCell('\u2500'))
		}
		for y := start + 1; y < end; y++ {
			canvas.set(0, y, nodeCell('\u2502'))
			canvas.set(frameCross-1, y, nodeCell('\u2502'))
		}
		canvas.set(0, start, nodeCell('\u250c'))
		canvas.set(frameCross-1, start, nodeCell('\u2510'))
		canvas.set(0, end, nodeCell('\u2514'))
		canvas.set(frameCross-1, end, nodeCell('\u2518'))
		x, y := 2, start
		if horizontal {
			x, y = start+2, 0
		}
		if err := putText(canvas.row(y), x, g.subgraphs[group].label); err != nil {
			return nil, err
		}
	}
	for i, lines := range labels {
		start := starts[i]
		end := start + sizes[i] - 1
		topLeft, topRight, bottomLeft, bottomRight := '\u250c', '\u2510', '\u2514', '\u2518'
		if g.nodes[i].shape == shapeStadium {
			topLeft, topRight, bottomLeft, bottomRight = '\u256d', '\u256e', '\u2570', '\u256f'
		}
		canvas.set(inset, start, nodeCell(topLeft))
		canvas.set(inset+boxCross-1, start, nodeCell(topRight))
		canvas.set(inset, end, nodeCell(bottomLeft))
		canvas.set(inset+boxCross-1, end, nodeCell(bottomRight))
		for x := 1; x < boxCross-1; x++ {
			canvas.set(inset+x, start, nodeCell('\u2500'))
			canvas.set(inset+x, end, nodeCell('\u2500'))
		}
		for y := start + 1; y < end; y++ {
			canvas.set(inset, y, nodeCell('\u2502'))
			canvas.set(inset+boxCross-1, y, nodeCell('\u2502'))
		}
		for j, line := range lines {
			x, y := inset+2, start+j+1
			if horizontal {
				x, y = start+2, inset+j+1
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
			canvas.set(lanes[i], y, edgeCell(symbol))
		}
	}
	for i, e := range g.edges {
		source, target := endpoints[i][0], endpoints[i][1]
		lane := lanes[i]
		for _, y := range []int{source, target} {
			endpoint := e.to
			if y == source {
				endpoint = e.from
			}
			for x := inset + boxCross; x < lane; x++ {
				ch := '\u2500'
				if e.dashed {
					ch = '\u2504'
				}
				if grouped && x == frameCross-1 && membership[endpoint] >= 0 {
					ch = '\u253c'
				} else if current := canvas.get(x, y); current == '\u2502' || current == '\u2506' {
					ch = '\u256a'
				}
				canvas.set(x, y, edgeCell(ch))
			}
		}
		canvas.set(inset+boxCross-1, source, nodeCell('\u251c'))
		canvas.set(inset+boxCross-1, target, nodeCell('\u251c'))
		canvas.set(inset+boxCross, source, edgeCell(e.sourceTip))
		canvas.set(inset+boxCross, target, edgeCell(e.targetTip))
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
			x, y := inset+boxCross+2, labelled.port
			if horizontal {
				x, y = labelled.port+1, inset+boxCross+1
			}
			for offset, line := range strings.Split(labelled.label, "\n") {
				if err := putText(canvas.row(y+offset), x, line); err != nil {
					return nil, err
				}
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

// portRows is the number of sub-rows one edge port occupies (Rust #62550): a
// vertical layout stacks every label line, while a horizontal layout strides by
// label width instead.
func portRows(horizontal bool, label string) int {
	if horizontal {
		return 1
	}
	return len(strings.Split(label, "\n"))
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
