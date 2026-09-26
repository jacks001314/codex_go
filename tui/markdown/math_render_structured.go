package markdown

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/markdown_render/math/render/structured.rs. Small,
// bounded layouts for aligned equations and common optimization notation. Cell
// parsing shares the parent parser's depth budget and preserves nested groups;
// display annotations and operator limits are centered above or below their base.

type mathAnnotationPosition int

const (
	mathAnnotationAbove mathAnnotationPosition = iota
	mathAnnotationBelow
)

// annotated centers an annotation above or below the base, sharing one width so
// the limits line up.
func (l mathLayout) annotated(annotation mathLayout, position mathAnnotationPosition) (mathLayout, bool) {
	width := l.width()
	if annotationWidth := annotation.width(); annotationWidth > width {
		width = annotationWidth
	}
	if len(l.rows)+len(annotation.rows) > mathMaxRows || width > mathMaxColumns {
		return mathLayout{}, false
	}
	baseline := l.baseline
	if position == mathAnnotationAbove {
		baseline += len(annotation.rows)
	}
	parts := []mathLayout{l, annotation}
	if position == mathAnnotationAbove {
		parts = []mathLayout{annotation, l}
	}
	rows := []string{}
	for _, part := range parts {
		padding := strings.Repeat(" ", (width-part.width())/2)
		for _, row := range part.rows {
			rows = append(rows, padding+row)
		}
	}
	return mathLayout{rows: rows, baseline: baseline}, true
}

// compactArgument parses a single-line child layout: compact limits are retained
// without flattening nested fractions (Rust's `compact_argument`).
func (p *mathParser) compactArgument() (mathLayout, bool) {
	stackAnnotations := p.stackAnnotations
	p.stackAnnotations = false
	argument, ok := p.argument()
	p.stackAnnotations = stackAnnotations
	return argument, ok
}

// displayOperator mirrors Rust's `display_operator`: a `sum`/`bigwedge` under
// display math stacks its `_`/`^` limits above and below the centered base.
func (p *mathParser) displayOperator(symbol string) (mathLayout, bool) {
	var lower *mathLayout
	var upper *mathLayout
	for {
		remaining := strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		if remaining == "" {
			break
		}
		isLower := remaining[0] == '_'
		if !isLower && remaining[0] != '^' {
			break
		}
		if isLower {
			if lower != nil {
				return mathLayout{}, false
			}
		} else if upper != nil {
			return mathLayout{}, false
		}
		p.remaining = remaining[1:]
		argument, ok := p.compactArgument()
		if !ok {
			return mathLayout{}, false
		}
		if isLower {
			lower = &argument
		} else {
			upper = &argument
		}
	}
	width := 1
	if lower != nil {
		width = lower.width()
	}
	if upper != nil && upper.width() > width {
		width = upper.width()
	}
	if width < 1 {
		width = 1
	}
	base := mathText(centerByRunes(symbol, width))
	if lower != nil {
		annotated, ok := base.annotated(*lower, mathAnnotationBelow)
		if !ok {
			return mathLayout{}, false
		}
		base = annotated
	}
	if upper != nil {
		annotated, ok := base.annotated(*upper, mathAnnotationAbove)
		if !ok {
			return mathLayout{}, false
		}
		base = annotated
	}
	return base, true
}

// structuredCommand mirrors Rust's `structured_command`.
func (p *mathParser) structuredCommand(name string) (mathLayout, bool) {
	switch name {
	case "begin":
		remaining := strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		if !strings.HasPrefix(remaining, "{aligned}") {
			return mathLayout{}, false
		}
		p.remaining = remaining[len("{aligned}"):]
		if strings.HasPrefix(strings.TrimLeftFunc(p.remaining, unicode.IsSpace), "[") {
			return mathLayout{}, false
		}
		return p.aligned()
	case "boxed":
		if !p.display {
			return mathLayout{}, false
		}
		inner, ok := p.argument()
		if !ok {
			return mathLayout{}, false
		}
		width := inner.width()
		if len(inner.rows)+2 > mathMaxRows || width+4 > mathMaxColumns {
			return mathLayout{}, false
		}
		border := strings.Repeat("─", width+2)
		rows := []string{"┌" + border + "┐"}
		for _, row := range inner.rows {
			padding := strings.Repeat(" ", width-codextui.DisplayWidth(row))
			rows = append(rows, "│ "+row+padding+" │")
		}
		rows = append(rows, "└"+border+"┘")
		return mathLayout{rows: rows, baseline: inner.baseline + 1}, true
	case "underset", "overset":
		annotation, ok := p.compactArgument()
		if !ok {
			return mathLayout{}, false
		}
		base, ok := p.argument()
		if !ok {
			return mathLayout{}, false
		}
		if p.stackAnnotations {
			position := mathAnnotationAbove
			if name == "underset" {
				position = mathAnnotationBelow
			}
			return base.annotated(annotation, position)
		}
		annotationText, ok := annotation.single()
		if !ok {
			return mathLayout{}, false
		}
		baseText, ok := base.single()
		if !ok {
			return mathLayout{}, false
		}
		marker := '^'
		if name == "underset" {
			marker = '_'
		}
		// Parenthesize the base so an annotation cannot attach to only its last atom.
		return mathText("(" + baseText + ")" + string(marker) + "{" + annotationText + "}"), true
	case "substack":
		remaining := strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		if !strings.HasPrefix(remaining, "{") {
			return mathLayout{}, false
		}
		p.remaining = remaining[1:]
		rows := []string{}
		for {
			if len(rows) == mathMaxRows {
				return mathLayout{}, false
			}
			p.remaining = strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
			row, ok := p.sequence(mathSeqCell)
			if !ok {
				return mathLayout{}, false
			}
			text, single := row.single()
			if !single {
				return mathLayout{}, false
			}
			rows = append(rows, strings.TrimSpace(text))
			if strings.HasPrefix(p.remaining, "}") {
				p.remaining = p.remaining[1:]
				break
			}
			if !strings.HasPrefix(p.remaining, `\\`) {
				return mathLayout{}, false
			}
			p.remaining = p.remaining[2:]
		}
		return mathText("(" + strings.Join(rows, "; ") + ")"), true
	case "mathcal":
		arg, ok := p.argument()
		if !ok {
			return mathLayout{}, false
		}
		text, single := arg.single()
		if !single {
			return mathLayout{}, false
		}
		mapped, ok := mapScriptText(text, "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "𝒜ℬ𝒞𝒟ℰℱ𝒢ℋℐ𝒥𝒦ℒℳ𝒩𝒪𝒫𝒬ℛ𝒮𝒯𝒰𝒱𝒲𝒳𝒴𝒵")
		if !ok {
			return mathLayout{}, false
		}
		return mathText(mapped), true
	default:
		return mathLayout{}, false
	}
}

// aligned mirrors Rust's `aligned`: display-only aligned blocks composed at their
// first row's baseline, with alternate cells aligned left and right.
func (p *mathParser) aligned() (mathLayout, bool) {
	if !p.display {
		return mathLayout{}, false
	}
	rows := [][]mathLayout{}
	cells := []mathLayout{}
	widths := []int{}
	for {
		p.remaining = strings.TrimLeftFunc(p.remaining, unicode.IsSpace)
		// A trailing row separator does not introduce an extra blank row.
		if len(cells) == 0 && strings.HasPrefix(p.remaining, `\end{aligned}`) {
			p.remaining = p.remaining[len(`\end{aligned}`):]
			break
		}
		// Bound empty columns and rows as well as their eventual rendered size.
		if len(rows) == mathMaxRows || len(cells) == 16 {
			return mathLayout{}, false
		}
		cell, ok := p.sequence(mathSeqCell)
		if !ok {
			return mathLayout{}, false
		}
		if len(cell.rows) == 1 {
			cell.rows[0] = strings.TrimSpace(cell.rows[0])
		}
		if len(widths) == len(cells) {
			widths = append(widths, cell.width())
		} else if cell.width() > widths[len(cells)] {
			widths[len(cells)] = cell.width()
		}
		cells = append(cells, cell)
		if strings.HasPrefix(p.remaining, "&") {
			p.remaining = p.remaining[1:]
			continue
		}
		if strings.HasPrefix(p.remaining, `\end{aligned}`) {
			p.remaining = p.remaining[len(`\end{aligned}`):]
			rows = append(rows, cells)
			break
		}
		if !strings.HasPrefix(p.remaining, `\\`) {
			return mathLayout{}, false
		}
		p.remaining = strings.TrimLeftFunc(p.remaining[2:], unicode.IsSpace)
		if strings.HasPrefix(p.remaining, "[") {
			distance, remaining, found := strings.Cut(p.remaining[1:], "]")
			if !found {
				return mathLayout{}, false
			}
			if !validTeXSpacing(distance) {
				return mathLayout{}, false
			}
			// Physical TeX spacing has no terminal equivalent; retain the row break.
			p.remaining = remaining
		}
		rows = append(rows, cells)
		cells = []mathLayout{}
	}
	output := []string{}
	layoutBaseline := 0
	for _, rowCells := range rows {
		baseline := -1
		for _, cell := range rowCells {
			if baseline < 0 || cell.baseline > baseline {
				baseline = cell.baseline
			}
		}
		if baseline < 0 {
			return mathLayout{}, false
		}
		if len(output) == 0 {
			layoutBaseline = baseline
		}
		height := 0
		for _, cell := range rowCells {
			if cellHeight := baseline + len(cell.rows) - cell.baseline; cellHeight > height {
				height = cellHeight
			}
		}
		if len(output)+height > mathMaxRows {
			return mathLayout{}, false
		}
		for lineIndex := 0; lineIndex < height; lineIndex++ {
			var line strings.Builder
			for index, cell := range rowCells {
				text := ""
				if rowIndex := lineIndex - (baseline - cell.baseline); rowIndex >= 0 && rowIndex < len(cell.rows) {
					text = cell.rows[rowIndex]
				}
				leading := 0
				if index%2 == 0 {
					leading = widths[index] - cell.width()
				}
				line.WriteString(strings.Repeat(" ", leading))
				line.WriteString(text)
				line.WriteString(strings.Repeat(" ", widths[index]-leading-codextui.DisplayWidth(text)))
				if index+1 < len(rowCells) {
					if index%2 == 0 {
						line.WriteString(" ")
					} else {
						line.WriteString("  ")
					}
				}
			}
			if codextui.DisplayWidth(line.String()) > mathMaxColumns {
				return mathLayout{}, false
			}
			output = append(output, strings.TrimRight(line.String(), " "))
		}
	}
	return mathLayout{rows: output, baseline: layoutBaseline}, true
}

// validTeXSpacing reports whether a `\\[<length><unit>]` row spacing has a known
// unit and a finite numeric prefix (Rust parses the distance the same way).
func validTeXSpacing(distance string) bool {
	units := []string{"pt", "pc", "in", "bp", "cm", "mm", "dd", "cc", "sp", "em", "ex"}
	trimmed := strings.TrimSpace(distance)
	for _, unit := range units {
		number, ok := strings.CutSuffix(trimmed, unit)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err != nil || value != value || value > 1e308 || value < -1e308 {
			return false
		}
		return true
	}
	return false
}

// centerByRunes mirrors Rust's `format!("{:^width$}", text)`, which pads by
// character count.
func centerByRunes(text string, width int) string {
	count := utf8.RuneCountInString(text)
	if count >= width {
		return text
	}
	padding := width - count
	left := padding / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
}
