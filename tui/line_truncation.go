package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/line_truncation.rs.

func DisplayWidth(text string) int {
	if printableASCII(text) {
		return len(text)
	}
	if strings.Contains(text, "\x1b") {
		return ansi.StringWidth(text)
	}
	width := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		width += displayWidthOfGrapheme(graphemes.Str())
	}
	return width
}

// displayWidthOfGrapheme measures one grapheme cluster the way DisplayWidth
// does, counting halfwidth-katakana voicing marks as their own columns.
func displayWidthOfGrapheme(grapheme string) int {
	marks := 0
	var base strings.Builder
	for _, r := range grapheme {
		if r == '\uFF9E' || r == '\uFF9F' {
			marks++
			continue
		}
		base.WriteRune(r)
	}
	return runewidth.StringWidth(base.String()) + marks
}

// printableASCII reports whether every byte is a printable ASCII character.
// Each of those occupies exactly one column, so callers can skip grapheme
// segmentation entirely; uniseg dominated resume-time rendering of long
// sessions.
func printableASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < 0x20 || text[i] > 0x7e {
			return false
		}
	}
	return true
}

func TruncateToWidth(text string, maxWidth int) string {
	if maxWidth <= 0 || text == "" {
		return ""
	}
	if strings.Contains(text, "\x1b") {
		// uniseg may split a long CSI parameter sequence at the width boundary.
		// Let the ANSI-aware truncator keep the escape intact and close any
		// active style at the cut.
		if ansi.StringWidth(text) <= maxWidth {
			return text
		}
		return ansi.Truncate(text, maxWidth, "")
	}
	used := 0
	var out strings.Builder
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		width := DisplayWidth(grapheme)
		if used+width > maxWidth {
			break
		}
		out.WriteString(grapheme)
		used += width
	}
	return out.String()
}

func TruncateWithEllipsis(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if strings.Contains(text, "\x1b") {
		if ansi.StringWidth(text) <= maxWidth {
			return text
		}
		if maxWidth == 1 {
			return "…"
		}
		return ansi.Truncate(text, maxWidth, "…")
	}
	if DisplayWidth(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	return TruncateToWidth(text, maxWidth-1) + "…"
}

// MiddleLabel mirrors Rust line_truncation::middle_label: a label too wide for
// its column keeps both ends and loses the middle, so the distinguishing
// directory or extension stays visible.
func MiddleLabel(text string, width int) string {
	return middleLabelWithSuffix(text, width, 0)
}

// DocumentLabel mirrors Rust line_truncation::document_label. Parts are ordered
// from the filename toward the root, matching the sidebar document tabs. A tab
// too narrow for the whole path keeps the directory suffix next to the file
// name so sibling tabs stay distinguishable.
func DocumentLabel(parts []string, width int) string {
	reversed := make([]string, 0, len(parts))
	for index := len(parts) - 1; index >= 0; index-- {
		reversed = append(reversed, parts[index])
	}
	name := strings.Join(reversed, "/")
	if DisplayWidth(name) <= width {
		return name
	}
	if len(parts) < 2 || width < 4 {
		return FilenameLabel(name, width)
	}
	directory := parts[len(parts)-1]
	budget := width / 2
	if directoryWidth := DisplayWidth(directory); directoryWidth < budget {
		budget = directoryWidth
	}
	directory = MiddleLabel(directory, budget)
	filename := FilenameLabel(parts[0], width-DisplayWidth(directory)-1)
	return directory + "/" + filename
}

// FilenameLabel mirrors Rust line_truncation::filename_label: middle_label that
// always keeps the extension, when the name has one.
func FilenameLabel(name string, width int) string {
	suffixWidth := 0
	if index := strings.LastIndex(name, "."); index >= 0 {
		ext := name[index+1:]
		if !strings.ContainsAny(ext, `/\`) {
			suffixWidth = DisplayWidth(ext) + 1
		}
	}
	return middleLabelWithSuffix(name, width, suffixWidth)
}

func middleLabelWithSuffix(text string, width int, suffixWidth int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(text) <= width {
		return text
	}
	prefix := (width - 1) / 2
	if suffixWidth < width-1 && prefix > width-1-suffixWidth {
		prefix = width - 1 - suffixWidth
	}
	label, rest, prefixWidth := TakePrefixByWidth(text, prefix)
	remaining := width - prefixWidth - 1
	suffix := len(rest)
	offsets := make([]int, 0, 8)
	widths := make([]int, 0, 8)
	graphemes := uniseg.NewGraphemes(rest)
	for graphemes.Next() {
		from, _ := graphemes.Positions()
		offsets = append(offsets, from)
		widths = append(widths, DisplayWidth(graphemes.Str()))
	}
	for index := len(offsets) - 1; index >= 0; index-- {
		if widths[index] > remaining {
			break
		}
		remaining -= widths[index]
		suffix = offsets[index]
	}
	return label + "…" + rest[suffix:]
}
