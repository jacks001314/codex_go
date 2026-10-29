package streaming

// Rust parity: codex-rs/tui/src/streaming/prose_preview.rs `LinkPreview`
// (#47589). Rich prose previews keep a link's label visible and withhold the
// opening parenthesis plus destination until the link closes, so finishing a
// long URL cannot collapse rows that were already shown.

// linkPreview scans append-only source incrementally; it never buffers or
// rescans a growing destination.
type linkPreview struct {
	brackets         int
	closedLabel      bool
	destination      bool
	destinationStart int
	depth            int
	delimiter        byte
	whitespace       bool
	escaped          bool
	codeTicks        int
	pendingTicks     int
}

func (l *linkPreview) reset() {
	*l = linkPreview{}
}

// scan consumes source[start:] and updates the pending-destination state.
func (l *linkPreview) scan(source string, start int) {
	if start < 0 {
		start = 0
	}
	if start > len(source) {
		start = len(source)
	}
	for index := start; index < len(source); index++ {
		value := source[index]
		if !l.destination {
			if value == '`' && (l.codeTicks > 0 || !l.escaped) {
				l.pendingTicks++
				l.closedLabel = false
				continue
			}
			if l.pendingTicks > 0 {
				if l.codeTicks == 0 {
					l.codeTicks = l.pendingTicks
				} else if l.codeTicks == l.pendingTicks {
					l.codeTicks = 0
				}
				l.pendingTicks = 0
			}
			if l.codeTicks > 0 {
				continue
			}
		}
		if l.escaped {
			l.escaped = false
			l.closedLabel = false
			l.whitespace = false
			continue
		}
		if value == '\\' {
			l.escaped = true
			l.closedLabel = false
			continue
		}
		if l.destination {
			switch {
			case l.delimiter != 0:
				if value == l.delimiter {
					l.delimiter = 0
				}
			case value == '<' && l.depth == 1 && (index == l.destinationStart+1 || l.whitespace):
				l.delimiter = '>'
			case (value == '\'' || value == '"') && l.whitespace && l.depth == 1:
				l.delimiter = value
			case value == '(':
				l.depth++
			case value == ')':
				l.depth--
				if l.depth == 0 {
					l.destination = false
				}
			}
			l.whitespace = isASCIIWhitespace(value)
			continue
		}
		switch value {
		case '[':
			l.brackets++
		case ']':
			if l.brackets > 0 {
				l.brackets--
				l.closedLabel = true
				continue
			}
		case '(':
			if l.closedLabel {
				l.destination = true
				l.destinationStart = index
				l.depth = 1
				l.whitespace = false
			}
		}
		l.closedLabel = false
	}
}

// pendingStart reports where an unfinished destination begins.
func (l *linkPreview) pendingStart() (int, bool) {
	if !l.destination {
		return 0, false
	}
	return l.destinationStart, true
}

func isASCIIWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\f' || value == '\r'
}
