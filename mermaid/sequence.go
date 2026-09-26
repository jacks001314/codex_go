package mermaid

import (
	"strings"
	"unicode"
)

// event is one ordered sequence timeline entry.
type sequenceEvent struct {
	kind   sequenceEventKind
	from   int
	to     int
	left   int
	right  int
	text   string
	dashed bool
	arrow  rune
}

type sequenceEventKind int

const (
	sequenceMessage sequenceEventKind = iota
	sequenceOpen
	sequenceBranch
	sequenceClose
	sequenceNote
)

type sequenceBlock struct {
	kind     string
	branched bool
}

// renderSequence draws bounded sequence timelines with ordered messages and
// explicitly nested control fragments (Rust `sequence::render`).
func renderSequence(body []string, maxWidth int) ([][]Span, error) {
	people := make([][3]string, 0, 8)
	declared := make([]bool, 0, 8)
	events := make([]sequenceEvent, 0, len(body))
	blocks := make([]sequenceBlock, 0, 4)
	for _, line := range body {
		rest := line
		if after, ok := cutAnyPrefix(line, "participant ", "actor "); ok {
			rest = after
			id, err := identifier(&rest)
			if err != nil {
				return nil, err
			}
			label := id
			if after, ok := strings.CutPrefix(strings.TrimLeftFunc(rest, unicode.IsSpace), "as "); ok {
				if err := checkLabel(after); err != nil {
					return nil, err
				}
				label = after
			} else if strings.TrimSpace(rest) != "" {
				return nil, ErrUnsupported
			}
			index, err := sequenceParticipant(&people, &declared, id)
			if err != nil {
				return nil, err
			}
			if declared[index] {
				return nil, ErrUnsupported
			}
			if strings.HasPrefix(line, "actor ") {
				people[index][1] = label + " (actor)"
			} else {
				people[index][1] = label
			}
			declared[index] = true
			continue
		}
		var event sequenceEvent
		switch {
		case line == "end":
			if len(blocks) == 0 {
				return nil, ErrUnsupported
			}
			blocks = blocks[:len(blocks)-1]
			event = sequenceEvent{kind: sequenceClose}
		case strings.HasPrefix(line, "else "):
			text := line[len("else "):]
			if len(blocks) == 0 || blocks[len(blocks)-1].kind != "alt" {
				return nil, ErrUnsupported
			}
			if blocks[len(blocks)-1].branched {
				return nil, ErrUnsupported
			}
			blocks[len(blocks)-1].branched = true
			if err := checkLabel(text); err != nil {
				return nil, err
			}
			event = sequenceEvent{kind: sequenceBranch, text: "else " + text}
		default:
			if kind, text, ok := sequenceBlockOpen(line); ok {
				if err := checkLabel(text); err != nil {
					return nil, err
				}
				if len(blocks) == 4 {
					return nil, ErrLimit
				}
				blocks = append(blocks, sequenceBlock{kind: kind})
				event = sequenceEvent{kind: sequenceOpen, text: kind + " " + text}
				break
			}
			if after, ok := cutAnyPrefix(line, "Note over ", "note over "); ok {
				rest = after
				id, err := identifier(&rest)
				if err != nil {
					return nil, err
				}
				left, err := sequenceParticipant(&people, &declared, id)
				if err != nil {
					return nil, err
				}
				right := left
				if after, ok := strings.CutPrefix(strings.TrimLeftFunc(rest, unicode.IsSpace), ","); ok {
					rest = after
					id, err := identifier(&rest)
					if err != nil {
						return nil, err
					}
					right, err = sequenceParticipant(&people, &declared, id)
					if err != nil {
						return nil, err
					}
				}
				text, ok := strings.CutPrefix(strings.TrimLeftFunc(rest, unicode.IsSpace), ":")
				if !ok {
					return nil, ErrUnsupported
				}
				text = strings.TrimSpace(text)
				if err := checkLabel(text); err != nil {
					return nil, err
				}
				event = sequenceEvent{
					kind:  sequenceNote,
					left:  minInt(left, right),
					right: maxInt(left, right),
					text:  text,
				}
				break
			}
			id, err := identifier(&rest)
			if err != nil {
				return nil, err
			}
			from, err := sequenceParticipant(&people, &declared, id)
			if err != nil {
				return nil, err
			}
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
			operators := []struct {
				token  string
				dashed bool
				arrow  rune
			}{
				{"-->>", true, '\u25b6'},
				{"->>", false, '\u25b6'},
				{"-->", true, '\u2504'},
				{"->", false, '\u2500'},
				{"--x", true, 'x'},
				{"-x", false, 'x'},
			}
			found := false
			var dashed bool
			var arrow rune
			for _, operator := range operators {
				if after, ok := strings.CutPrefix(rest, operator.token); ok {
					rest = after
					dashed, arrow, found = operator.dashed, operator.arrow, true
					break
				}
			}
			if !found {
				return nil, ErrUnsupported
			}
			id, err = identifier(&rest)
			if err != nil {
				return nil, err
			}
			to, err := sequenceParticipant(&people, &declared, id)
			if err != nil {
				return nil, err
			}
			text, ok := strings.CutPrefix(strings.TrimLeftFunc(rest, unicode.IsSpace), ":")
			if !ok {
				return nil, ErrUnsupported
			}
			text = strings.TrimSpace(text)
			if err := checkLabel(text); err != nil {
				return nil, err
			}
			event = sequenceEvent{kind: sequenceMessage, from: from, to: to, text: text, dashed: dashed, arrow: arrow}
		}
		if len(events) == 64 {
			return nil, ErrLimit
		}
		events = append(events, event)
	}
	if len(blocks) != 0 || len(people) == 0 {
		return nil, ErrUnsupported
	}
	nameWidth := 0
	for _, person := range people {
		if w := textWidth(person[1]); w > nameWidth {
			nameWidth = w
		}
	}
	textWidthMax := 0
	for _, event := range events {
		var w int
		switch event.kind {
		case sequenceMessage, sequenceOpen, sequenceBranch:
			w = textWidth(event.text)
		case sequenceNote:
			w = textWidth(event.text) + 6
		}
		if w > textWidthMax {
			textWidthMax = w
		}
	}
	boxWidth := nameWidth + 4
	stride := maxInt(boxWidth+2, textWidthMax+4)
	centers := make([]int, len(people))
	for i := range people {
		centers[i] = 10 + boxWidth/2 + stride*i
	}
	last := centers[len(people)-1]
	width := maxInt(last+boxWidth-boxWidth/2+10, last+textWidthMax+14)
	if width*(3+4*len(events)) > maxCells {
		return nil, ErrLimit
	}
	rows := make([][]cell, 3)
	for y := range rows {
		rows[y] = blankSequenceRow(width)
	}
	for i, person := range people {
		left := centers[i] - boxWidth/2
		right := left + boxWidth - 1
		rows[0][left] = nodeCell('\u250c')
		rows[0][right] = nodeCell('\u2510')
		rows[2][left] = nodeCell('\u2514')
		rows[2][right] = nodeCell('\u2518')
		fillCells(rows[0], left+1, right, nodeCell('\u2500'))
		fillCells(rows[2], left+1, right, nodeCell('\u2500'))
		rows[1][left] = nodeCell('\u2502')
		rows[1][right] = nodeCell('\u2502')
		if err := putText(rows[1], left+2, person[1]); err != nil {
			return nil, err
		}
	}
	depth := 0
	for _, event := range events {
		count := 3
		if event.kind == sequenceMessage && event.from == event.to {
			count = 4
		}
		top := len(rows)
		for i := 0; i < count; i++ {
			row := blankSequenceRow(width)
			for _, center := range centers {
				row[center] = edgeCell('\u2502')
			}
			for level := 0; level < depth; level++ {
				row[level*2] = nodeCell('\u2502')
				row[width-1-level*2] = nodeCell('\u2502')
			}
			rows = append(rows, row)
		}
		switch event.kind {
		case sequenceMessage:
			a, b := centers[event.from], centers[event.to]
			arrow := event.arrow
			if a >= b && arrow == '\u25b6' {
				arrow = '\u25c0'
			}
			stroke := '\u2500'
			if event.dashed {
				stroke = '\u2504'
			}
			if err := putText(rows[top], minInt(a, b)+2, event.text); err != nil {
				return nil, err
			}
			if a == b {
				fillCells(rows[top+1], a, a+3, edgeCell(stroke))
				rows[top+1][a] = edgeCell('\u251c')
				rows[top+1][a+4] = edgeCell('\u2510')
				fillCells(rows[top+2], a, a+3, edgeCell(stroke))
				rows[top+2][a+4] = edgeCell('\u2518')
				rows[top+2][a] = edgeCell(arrow)
			} else {
				fillCells(rows[top+1], minInt(a, b), maxInt(a, b), edgeCell(stroke))
				for _, center := range centers {
					if center > minInt(a, b) && center < maxInt(a, b) {
						rows[top+1][center] = edgeCell('\u253c')
					}
				}
				cross := '\u251c'
				if a > b {
					cross = '\u2524'
				}
				rows[top+1][a] = edgeCell(cross)
				rows[top+1][b] = edgeCell(arrow)
			}
		case sequenceNote:
			label := "Note: " + event.text
			start := centers[event.left]
			end := maxInt(centers[event.right], start+textWidth(label)+3)
			fillCells(rows[top], start, end, nodeCell('\u2500'))
			fillCells(rows[top+2], start, end, nodeCell('\u2500'))
			rows[top][start] = nodeCell('\u250c')
			rows[top][end] = nodeCell('\u2510')
			rows[top+2][start] = nodeCell('\u2514')
			rows[top+2][end] = nodeCell('\u2518')
			fillCells(rows[top+1], start, end, nodeCell(' '))
			rows[top+1][start] = nodeCell('\u2502')
			rows[top+1][end] = nodeCell('\u2502')
			if err := putText(rows[top+1], start+2, label); err != nil {
				return nil, err
			}
		case sequenceOpen, sequenceBranch:
			opening := !strings.HasPrefix(event.text, "else ")
			if opening {
				depth++
			}
			left := (depth - 1) * 2
			right := width - 1 - left
			fillCells(rows[top], left, right, nodeCell('\u2500'))
			leftCorner, rightCorner := '\u251c', '\u2524'
			if opening {
				leftCorner, rightCorner = '\u250c', '\u2510'
			}
			rows[top][left] = nodeCell(leftCorner)
			rows[top][right] = nodeCell(rightCorner)
			if err := putText(rows[top], left+2, event.text); err != nil {
				return nil, err
			}
			for _, row := range rows[top+1 : top+3] {
				row[left] = nodeCell('\u2502')
				row[right] = nodeCell('\u2502')
			}
		case sequenceClose:
			depth--
			left := depth * 2
			right := width - 1 - left
			fillCells(rows[top], left, right, nodeCell('\u2500'))
			rows[top][left] = nodeCell('\u2514')
			rows[top][right] = nodeCell('\u2518')
			for _, row := range rows[top+1 : top+3] {
				row[left] = nodeCell(' ')
				row[right] = nodeCell(' ')
			}
		}
	}
	for _, row := range rows {
		last := -1
		for x := len(row) - 1; x >= 0; x-- {
			if row[x].symbol != ' ' {
				last = x
				break
			}
		}
		if last >= maxWidth {
			return nil, ErrTooWide
		}
	}
	return finish(rows), nil
}

// sequenceBlockOpen recognizes `loop`/`alt`/`opt`/`critical`/`break` fragments.
func sequenceBlockOpen(line string) (string, string, bool) {
	index := strings.Index(line, " ")
	if index < 0 {
		return "", "", false
	}
	kind, text := line[:index], line[index+1:]
	switch kind {
	case "loop", "alt", "opt", "critical", "break":
		return kind, text, true
	default:
		return "", "", false
	}
}

func cutAnyPrefix(text string, prefixes ...string) (string, bool) {
	for _, prefix := range prefixes {
		if after, ok := strings.CutPrefix(text, prefix); ok {
			return after, true
		}
	}
	return "", false
}

func sequenceParticipant(people *[][3]string, declared *[]bool, id string) (int, error) {
	for index := range *people {
		if (*people)[index][0] == id {
			return index, nil
		}
	}
	if len(*people) == 8 {
		return 0, ErrLimit
	}
	*people = append(*people, [3]string{id, id, ""})
	*declared = append(*declared, false)
	return len(*people) - 1, nil
}

func blankSequenceRow(width int) []cell {
	row := make([]cell, width)
	for x := range row {
		row[x] = edgeCell(' ')
	}
	return row
}

// fillCells fills the inclusive range [start, end] with the given cell.
func fillCells(row []cell, start, end int, current cell) {
	if start < 0 {
		start = 0
	}
	if end >= len(row) {
		end = len(row) - 1
	}
	for x := start; x <= end; x++ {
		row[x] = current
	}
}
