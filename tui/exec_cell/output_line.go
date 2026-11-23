package execcell

// Rust parity: codex-rs/tui/src/exec_cell/output_line.rs.
//
// Resolve progress writes within one LF-delimited command output row before
// layout: CR returns to column zero, and erasing the line discards the
// unwritten suffix.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"codex_go/tui"

	"github.com/rivo/uniseg"
)

var (
	// ansi-to-tui only recognizes BEL as an OSC terminator.
	oscControlPattern = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\|\x1b?$)")
	csiControlPattern = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*(?:[@-~]|$)")
)

// PlainOutputText returns the visible text of a command output row for the /ps
// footer and historical command fallbacks.
func PlainOutputText(line string) string {
	if strings.ContainsRune(line, 0x1b) {
		line = stripTerminalEscapes(line)
	}
	return strings.TrimRightFunc(line, unicode.IsSpace)
}

// stripTerminalEscapes drops CSI and OSC control sequences the way
// ansi_escape_line does, keeping tabs and every other printable byte.
func stripTerminalEscapes(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] != 0x1b {
			out.WriteByte(text[index])
			index++
			continue
		}
		if index+1 >= len(text) {
			break
		}
		switch text[index+1] {
		case '[':
			cursor := index + 2
			for cursor < len(text) && (text[cursor] < 0x40 || text[cursor] > 0x7e) {
				cursor++
			}
			if cursor < len(text) {
				cursor++
			}
			index = cursor
		case ']':
			cursor := index + 2
			for cursor < len(text) {
				if text[cursor] == 0x07 {
					cursor++
					break
				}
				if text[cursor] == 0x1b && cursor+1 < len(text) && text[cursor+1] == '\\' {
					cursor += 2
					break
				}
				cursor++
			}
			index = cursor
		default:
			index += 2
		}
	}
	return out.String()
}

// CommandOutputLine resolves carriage-return redraws and line erases inside one
// LF-delimited row, preserving the ANSI styling of the text that survives.
func CommandOutputLine(raw string) string {
	raw = strings.TrimRight(raw, "\r")
	if strings.Contains(raw, "\x1b]") && (strings.ContainsRune(raw, '\r') || strings.Contains(raw, "\x1b\\")) {
		raw = oscControlPattern.ReplaceAllString(raw, "")
	}
	if !strings.ContainsRune(raw, '\r') && !strings.Contains(raw, "\x1b[2K") {
		return raw
	}

	// Later writes cover a prefix of every earlier write. Walk backwards so a
	// short repeated progress update never copies a long surviving tail twice.
	type fragment struct {
		offset int
		text   string
	}
	covered := 0
	styled := strings.ContainsRune(raw, 0x1b)
	var result strings.Builder
	fragments := []fragment{}
	for high := len(raw); high >= 0; {
		low := 0
		if index := strings.LastIndexByte(raw[:high], '\r'); index >= 0 {
			low = index + 1
		}
		write := raw[low:high]
		offset := low
		erased := false
		var column int
		var suffix string
		if isPlainASCII(write) && !strings.ContainsAny(write, "\x1b\t") {
			column = len(write)
			if covered < len(write) {
				suffix = write[covered:]
			}
		} else {
			var visible []byte
			// A whole-line erase blanks earlier columns without moving the cursor.
			clearAt := strings.LastIndex(write, "\x1b[2K")
			controls := csiControlPattern.FindAllStringIndex(write, -1)
			start := 0
			visibleEnd := 0
			for index := 0; index <= len(controls); index++ {
				segmentEnd := len(write)
				var control []int
				if index < len(controls) {
					control = controls[index]
					segmentEnd = control[0]
				}
				graphemes := uniseg.NewGraphemes(write[start:segmentEnd])
				for graphemes.Next() {
					glyph := graphemes.Str()
					if glyph == "\x1b" {
						continue
					}
					width := tui.DisplayWidth(glyph)
					if glyph == "\t" {
						width = 4
					}
					if clearAt >= 0 && start < clearAt {
						column += width
						continue
					}
					if len(visible) == 0 {
						offset = low + start
					}
					switch {
					case column >= covered:
						visible = append(visible, glyph...)
					case column+width > covered:
						// Overwriting half a wide glyph clears its remaining cells.
						visible = append(visible, strings.Repeat(" ", column+width-covered)...)
					}
					column += width
					visibleEnd = len(visible)
				}
				if control == nil {
					continue
				}
				text := write[control[0]:control[1]]
				switch {
				case text == "\x1b[K" || text == "\x1b[0K":
					erased = true
				case text == "\x1b[2K" && control[0] == clearAt:
					erased = true
					offset = low + control[0]
					if pad := column - covered; pad > 0 {
						visible = append(visible, strings.Repeat(" ", pad)...)
					}
					visibleEnd = len(visible)
				case strings.HasSuffix(text, "m") && len(visible) > 0:
					visible = append(visible, text...)
				}
				start = control[1]
			}
			suffix = string(visible[:visibleEnd])
		}
		if column > covered {
			if styled {
				fragments = append(fragments, fragment{offset: offset, text: suffix})
			} else {
				result.WriteString(suffix)
			}
			covered = column
		}
		if erased || low == 0 {
			break
		}
		high = low - 1
	}

	if !styled {
		return result.String()
	}
	// Resolve styles in write order, then apply them in display order. Restore
	// the final cursor style because bounded previews reuse this row for later CRs.
	offsets := make([]int, 0, len(fragments)+1)
	for index := len(fragments) - 1; index >= 0; index-- {
		offsets = append(offsets, fragments[index].offset)
	}
	offsets = append(offsets, len(raw))
	styles := stylePrefixes(raw, offsets)
	cursor := styles[len(styles)-1]
	styles = styles[:len(styles)-1]
	var out strings.Builder
	current := []string{}
	currentValid := true
	apply := func(style []string, text string) {
		if !currentValid || !samePrefix(style, current) {
			if !(currentValid && len(current) == 0) {
				out.WriteString("\x1b[0m")
			}
			for _, code := range style {
				out.WriteString(code)
			}
		}
		out.WriteString(text)
		if strings.ContainsRune(text, 0x1b) {
			currentValid = false
			current = nil
			return
		}
		current = style
		currentValid = true
	}
	for index, frag := range fragments {
		apply(styles[len(fragments)-1-index], frag.text)
	}
	apply(cursor, "")
	return out.String()
}

func isPlainASCII(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] > 0x7f {
			return false
		}
	}
	return true
}

func samePrefix(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// stylePrefixes keeps an SGR only while it supplies an attribute that later
// SGRs leave alone. Codes are resolved against the same parse the renderer uses
// so compound codes and partial resets agree with layout.
func stylePrefixes(raw string, offsets []int) [][]string {
	type activeCode struct {
		sgr   string
		attrs map[string]string
	}
	active := []activeCode{}
	controls := csiControlPattern.FindAllStringIndex(raw, -1)
	controlIndex := 0
	prefixes := make([][]string, 0, len(offsets))
	for _, offset := range offsets {
		for controlIndex < len(controls) && controls[controlIndex][0] < offset {
			control := controls[controlIndex]
			controlIndex++
			sgr := raw[control[0]:control[1]]
			if !strings.HasSuffix(sgr, "m") {
				continue
			}
			if len(active) > 0 && active[0].sgr == sgr {
				continue
			}
			if sgr == "\x1b[m" || sgr == "\x1b[0m" {
				active = active[:0]
				continue
			}
			attrs := sgrAttributes(sgr[2 : len(sgr)-1])
			if len(attrs) == 0 {
				continue
			}
			// Newest first. Each retained code contributes at least one attribute.
			newer := map[string]string{}
			kept := active[:0]
			for _, code := range active {
				if codeContributes(code.attrs, newer) {
					kept = append(kept, code)
					for key, value := range code.attrs {
						if _, ok := newer[key]; !ok {
							newer[key] = value
						}
					}
				}
			}
			active = append([]activeCode{{sgr: sgr, attrs: attrs}}, kept...)
		}
		prefix := make([]string, 0, len(active))
		for index := len(active) - 1; index >= 0; index-- {
			prefix = append(prefix, active[index].sgr)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func codeContributes(older map[string]string, newer map[string]string) bool {
	for key := range older {
		if _, ok := newer[key]; !ok {
			return true
		}
	}
	return false
}

// sgrAttributes maps one SGR parameter list to the attributes it sets. Values
// are compared as identities, so "1" (bold on) and "0" (bold off) stay distinct
// from an attribute the code leaves untouched.
func sgrAttributes(params string) map[string]string {
	attrs := map[string]string{}
	numbers := make([]int, 0, 4)
	for _, part := range strings.Split(params, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			numbers = append(numbers, 0)
			continue
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return attrs
		}
		numbers = append(numbers, number)
	}
	for index := 0; index < len(numbers); index++ {
		number := numbers[index]
		switch {
		case number == 0:
			attrs = map[string]string{}
		case number == 1:
			attrs["bold"] = "1"
		case number == 2:
			attrs["dim"] = "1"
		case number == 3:
			attrs["italic"] = "1"
		case number == 4:
			attrs["underline"] = "1"
		case number == 5 || number == 6:
			attrs["blink"] = strconv.Itoa(number)
		case number == 7:
			attrs["reverse"] = "1"
		case number == 8:
			attrs["hidden"] = "1"
		case number == 9:
			attrs["strike"] = "1"
		case number == 22:
			attrs["bold"] = "0"
			attrs["dim"] = "0"
		case number == 23:
			attrs["italic"] = "0"
		case number == 24:
			attrs["underline"] = "0"
		case number == 25:
			attrs["blink"] = "0"
		case number == 27:
			attrs["reverse"] = "0"
		case number == 28:
			attrs["hidden"] = "0"
		case number == 29:
			attrs["strike"] = "0"
		case number >= 30 && number <= 37:
			attrs["fg"] = strconv.Itoa(number - 30)
		case number == 39:
			attrs["fg"] = "default"
		case number >= 40 && number <= 47:
			attrs["bg"] = strconv.Itoa(number - 40)
		case number == 49:
			attrs["bg"] = "default"
		case number >= 90 && number <= 97:
			attrs["fg"] = "bright:" + strconv.Itoa(number-90)
		case number >= 100 && number <= 107:
			attrs["bg"] = "bright:" + strconv.Itoa(number-100)
		case number == 38 || number == 48:
			value, consumed, ok := extendedSGRColor(numbers[index+1:])
			if !ok {
				continue
			}
			if number == 38 {
				attrs["fg"] = value
			} else {
				attrs["bg"] = value
			}
			index += consumed
		}
	}
	return attrs
}

func extendedSGRColor(rest []int) (string, int, bool) {
	if len(rest) == 0 {
		return "", 0, false
	}
	switch rest[0] {
	case 5:
		if len(rest) < 2 {
			return "", 0, false
		}
		return "idx:" + strconv.Itoa(rest[1]), 2, true
	case 2:
		if len(rest) < 4 {
			return "", 0, false
		}
		return "rgb:" + strconv.Itoa(rest[1]) + "," + strconv.Itoa(rest[2]) + "," + strconv.Itoa(rest[3]), 4, true
	default:
		return "", 0, false
	}
}

// PendingLine keeps the unfinished row across output deltas so the /ps footer
// and replayed background terminals show the latest visible text. Rust stores
// the same state in a bounded head/tail buffer; Go already caps the aggregated
// output at MaxLiveOutputBytes per call.
type PendingLine struct {
	row string
}

// PushLines yields the rows completed by this chunk and keeps the unfinished
// row for the next delta.
func (p *PendingLine) PushLines(chunk string) []string {
	parts := strings.Split(chunk, "\n")
	completed := parts[:len(parts)-1]
	partial := parts[len(parts)-1]
	rows := make([]string, 0, len(completed))
	for _, part := range completed {
		rows = append(rows, CommandOutputLine(p.row+part))
		p.row = ""
	}
	p.row += partial
	return rows
}

// Render resolves the unfinished row to its visible text.
func (p PendingLine) Render() string {
	return CommandOutputLine(p.row)
}
