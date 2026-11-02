package historycell

import (
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/terminal_hyperlinks.rs HyperlinkLine,
// plain_hyperlink_lines, adaptive_wrap_hyperlink_lines and visible_lines
// (#58446). A history cell that knows where its links are keeps those column
// ranges while the line is truncated or wrapped, so the terminal never receives
// a link that starts or ends in the wrong place.

// PlainHyperlinkLines wraps plain rendered lines without any hyperlinks.
func PlainHyperlinkLines(lines []string) []HyperlinkLine {
	out := make([]HyperlinkLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, HyperlinkLine{Text: line})
	}
	return out
}

// RenderHyperlinkLines serializes hyperlink-aware lines to terminal strings with
// OSC-8 sequences for every link, in source column order.
func RenderHyperlinkLines(lines []HyperlinkLine) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, renderHyperlinkLine(line))
	}
	return out
}

func renderHyperlinkLine(line HyperlinkLine) string {
	links := make([]tui.TerminalHyperlink, 0, len(line.Links))
	for _, link := range line.Links {
		if link.End <= link.Start || link.Start < 0 || strings.TrimSpace(link.Destination) == "" {
			continue
		}
		links = append(links, link)
	}
	if len(links) == 0 {
		return line.Text
	}
	sort.SliceStable(links, func(left, right int) bool { return links[left].Start < links[right].Start })
	var builder strings.Builder
	cursor := 0
	for _, link := range links {
		start := byteOffsetForColumn(line.Text, link.Start)
		end := byteOffsetForColumn(line.Text, link.End)
		if start < cursor {
			start = cursor
		}
		if end <= start {
			continue
		}
		builder.WriteString(line.Text[cursor:start])
		builder.WriteString(osc8ForDestination(link.Destination, line.Text[start:end]))
		cursor = end
	}
	builder.WriteString(line.Text[cursor:])
	return builder.String()
}

// osc8ForDestination keeps web destinations on the web-only serializer and
// sends validated local file destinations through the file serializer.
func osc8ForDestination(destination string, text string) string {
	if parsed, err := url.Parse(destination); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return tui.OSC8Hyperlink(destination, text)
	}
	return tui.OSC8FileHyperlink(destination, text)
}

// ClipHyperlinkLine truncates a line to width columns and drops or shortens the
// links that fall outside the visible part (Rust clip_hyperlinks_to_width).
func ClipHyperlinkLine(line HyperlinkLine, width int) HyperlinkLine {
	if width <= 0 {
		return HyperlinkLine{}
	}
	clipped := HyperlinkLine{Text: tui.TruncateWithEllipsis(line.Text, width), Links: nil}
	visible := tui.DisplayWidth(clipped.Text)
	for _, link := range tui.ClipHyperlinksToWidth(line.Links, visible) {
		clipped.Links = append(clipped.Links, link)
	}
	return clipped
}

// AdaptiveWrapHyperlinkLines wraps each logical line with the existing adaptive
// wrapper and remaps its link columns onto the wrapped rows (Rust
// adaptive_wrap_hyperlink_lines / remap_source_wrapped_line).
func AdaptiveWrapHyperlinkLines(lines []HyperlinkLine, options tui.WrapOptions) []HyperlinkLine {
	out := []HyperlinkLine{}
	for index, line := range lines {
		lineOptions := options
		if index > 0 {
			lineOptions.InitialIndent = options.SubsequentIndent
		}
		rows := tui.AdaptiveWrapLine(line.Text, lineOptions)
		out = append(out, remapHyperlinksOntoRows(line, rows, lineOptions)...)
	}
	return out
}

func remapHyperlinksOntoRows(line HyperlinkLine, rows []string, options tui.WrapOptions) []HyperlinkLine {
	source := line.Text
	position := 0
	out := make([]HyperlinkLine, 0, len(rows))
	for index, row := range rows {
		indent := options.InitialIndent
		if index > 0 {
			indent = options.SubsequentIndent
		}
		out = append(out, remapRow(line, source, row, indent, position))
		content := strings.TrimRight(strings.TrimPrefix(row, indent), " \t")
		if content == "" {
			continue
		}
		if start := strings.Index(source[position:], content); start >= 0 {
			position += start + len(content)
		}
	}
	return out
}

func remapRow(line HyperlinkLine, source string, row string, indent string, position int) HyperlinkLine {
	remapped := HyperlinkLine{Text: row}
	content := strings.TrimRight(strings.TrimPrefix(row, indent), " \t")
	if content == "" {
		return remapped
	}
	start := strings.Index(source[position:], content)
	if start < 0 {
		return remapped
	}
	start += position
	end := start + len(content)
	indentWidth := tui.DisplayWidth(indent)
	for _, link := range line.Links {
		linkStart := byteOffsetForColumn(source, link.Start)
		linkEnd := byteOffsetForColumn(source, link.End)
		visibleStart := maxInt(linkStart, start)
		visibleEnd := minInt(linkEnd, end)
		if visibleEnd <= visibleStart {
			continue
		}
		remapped.Links = append(remapped.Links, tui.TerminalHyperlink{
			Start:       indentWidth + tui.DisplayWidth(source[start:visibleStart]),
			End:         indentWidth + tui.DisplayWidth(source[start:visibleEnd]),
			Destination: link.Destination,
		})
	}
	return remapped
}

func byteOffsetForColumn(text string, column int) int {
	if column <= 0 {
		return 0
	}
	width := 0
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		next := width + tui.DisplayWidth(string(r))
		if next > column {
			return index
		}
		width = next
		index += size
		if width == column {
			return index
		}
	}
	return len(text)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
