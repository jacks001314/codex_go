package tea

import (
	"strings"

	"codex_go/tui"
	"codex_go/tui/bottom_pane"
)

// paneStatusSpan is one styled run of a thread status line retained for side
// panes (Rust #61141 keeps structured content so it can reflow).
type paneStatusSpan struct {
	text string
	sgr  string
}

const (
	paneStatusDimSGR   = "\x1b[2m"
	paneStatusResetSGR = "\x1b[0m"
)

func paneStatusSpansFromLine(line bottompane.StatusLine, threadColor string, theme string) []paneStatusSpan {
	spans := make([]paneStatusSpan, 0, len(line.Spans))
	for _, span := range line.Spans {
		sgr := ""
		switch {
		case span.ThreadIdentity && threadColor != "":
			sgr = threadColor
		case span.Accent != "" && span.Accent != bottompane.StatusLineAccentNone:
			sgr = statusLineAccentSGR(theme, span.Accent)
			if sgr == "" {
				sgr = bottompane.StatusLineFallbackSGR(span.Accent)
			}
		}
		if span.Dim {
			sgr = paneStatusDimSGR + sgr
		}
		spans = append(spans, paneStatusSpan{text: span.Text, sgr: sgr})
	}
	return spans
}

// wrapPaneStatusSpans wraps structured status content to width while keeping
// each span's SGR. Rows stay dimmed and reset at the end.
func wrapPaneStatusSpans(spans []paneStatusSpan, width int) []string {
	if width <= 0 || len(spans) == 0 {
		return nil
	}
	var rows []string
	var line strings.Builder
	lineWidth := 0
	flush := func() {
		if line.Len() == 0 {
			return
		}
		rows = append(rows, paneStatusDimSGR+line.String()+paneStatusResetSGR)
		line.Reset()
		lineWidth = 0
	}
	appendText := func(text string, sgr string) {
		if text == "" {
			return
		}
		if sgr == "" {
			line.WriteString(text)
			return
		}
		line.WriteString(sgr)
		line.WriteString(text)
		line.WriteString(paneStatusResetSGR)
		line.WriteString(paneStatusDimSGR)
	}
	for _, span := range spans {
		for _, field := range strings.SplitAfter(span.text, " ") {
			if field == "" {
				continue
			}
			fieldWidth := tui.DisplayWidth(field)
			if lineWidth > 0 && lineWidth+fieldWidth > width {
				flush()
				field = strings.TrimLeft(field, " ")
				fieldWidth = tui.DisplayWidth(field)
				if field == "" {
					continue
				}
			}
			if fieldWidth > width {
				for _, chunk := range hardWrapPaneStatusText(field, width) {
					flush()
					appendText(chunk, span.sgr)
					lineWidth = tui.DisplayWidth(chunk)
					flush()
				}
				continue
			}
			appendText(field, span.sgr)
			lineWidth += fieldWidth
		}
	}
	flush()
	return rows
}

func hardWrapPaneStatusText(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var chunks []string
	var current strings.Builder
	currentWidth := 0
	for _, r := range text {
		runeWidth := tui.DisplayWidth(string(r))
		if currentWidth+runeWidth > width && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
			currentWidth = 0
		}
		current.WriteRune(r)
		currentWidth += runeWidth
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}
