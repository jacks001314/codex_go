package tea

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	codextui "codex_go/tui"
)

// Rust #62390: the fullscreen TUI recolours the link painted under the pointer
// so the clickable region is visible before a click. Go paints rows as strings,
// so the highlight is applied to the finished row: the OSC 8 span under the
// pointer is located by display column and its foreground is swapped until the
// span ends, then restored to the sequence that was active when it began.
// The highlight is recomputed on every draw from the stored pointer position,
// so scrolling or new output cannot leave it on stale content.

type paintedLinkSpan struct {
	// bodyStart and bodyEnd bound the link text inside its OSC 8 wrapper.
	bodyStart int
	bodyEnd   int
	// restore is the SGR sequence active when the link text began.
	restore string
}

// paintedLinkSpanAt reports the OSC 8 link painted at column col of row.
// Wide glyphs keep the highlight aligned because columns advance by the
// rune's display width, and a link that is still open at the end of the row is
// treated as extending to the last painted column.
func paintedLinkSpanAt(row string, col int) (paintedLinkSpan, bool) {
	if col < 0 || row == "" {
		return paintedLinkSpan{}, false
	}
	column := 0
	sgr := ""
	linkOpen := -1
	linkStartColumn := 0
	linkRestore := ""
	inLink := false
	for i := 0; i < len(row); {
		if strings.HasPrefix(row[i:], osc8Prefix) {
			bodyStart, hasURL, ok := parseOSC8(row, i)
			if !ok {
				return paintedLinkSpan{}, false
			}
			if hasURL {
				if !inLink {
					inLink = true
					linkOpen = bodyStart
					linkStartColumn = column
					linkRestore = sgr
				}
			} else if inLink {
				if col >= linkStartColumn && col < column {
					return paintedLinkSpan{bodyStart: linkOpen, bodyEnd: i, restore: linkRestore}, true
				}
				inLink = false
			}
			i = bodyStart
			continue
		}
		if strings.HasPrefix(row[i:], "\x1b[") {
			end := csiEnd(row, i)
			if end < 0 {
				return paintedLinkSpan{}, false
			}
			if row[end-1] == 'm' {
				sgr = row[i:end]
			}
			i = end
			continue
		}
		if row[i] == 0x1b {
			// An escape this TUI does not paint over (another OSC, DCS, or a
			// truncated sequence) ends the scan; nothing after it is a link.
			return paintedLinkSpan{}, false
		}
		r, size := utf8.DecodeRuneInString(row[i:])
		width := runewidth.RuneWidth(r)
		if width < 0 {
			width = 0
		}
		column += width
		i += size
	}
	if inLink && col >= linkStartColumn && col < column {
		return paintedLinkSpan{bodyStart: linkOpen, bodyEnd: len(row), restore: linkRestore}, true
	}
	return paintedLinkSpan{}, false
}

// restylePaintedLink paints fg over one link span and restores the surrounding
// foreground afterwards.
func restylePaintedLink(row string, span paintedLinkSpan, fg string) string {
	if fg == "" || span.bodyStart < 0 || span.bodyEnd > len(row) || span.bodyStart > span.bodyEnd {
		return row
	}
	var builder strings.Builder
	builder.Grow(len(row) + len(fg) + len(span.restore))
	builder.WriteString(row[:span.bodyStart])
	builder.WriteString(fg)
	builder.WriteString(row[span.bodyStart:span.bodyEnd])
	builder.WriteString(span.restore)
	builder.WriteString(row[span.bodyEnd:])
	return builder.String()
}

const osc8Prefix = "\x1b]8;;"

// parseOSC8 splits one OSC 8 sequence starting at index i. It returns the raw
// index where the link body starts and whether the sequence opens a link (a
// closing marker carries an empty URL).
func parseOSC8(row string, i int) (bodyStart int, hasURL bool, ok bool) {
	urlStart := i + len(osc8Prefix)
	for k := urlStart; k < len(row); k++ {
		if row[k] == '\a' {
			return k + 1, urlStart < k, true
		}
		if row[k] == 0x1b && k+1 < len(row) && row[k+1] == '\\' {
			return k + 2, urlStart < k, true
		}
	}
	return 0, false, false
}

// csiEnd returns the index just past a CSI sequence that starts at i, or -1.
func csiEnd(row string, i int) int {
	for k := i + 2; k < len(row); k++ {
		if row[k] >= 0x40 && row[k] <= 0x7e {
			return k + 1
		}
	}
	return -1
}

// linkHoverApplies reports whether the pointer may highlight a painted link:
// the TUI owns the screen and no overlay, modal, dashboard or picker owns the
// input yet (Rust #62390 hover_pointer).
func (m *Model) linkHoverApplies() bool {
	if m == nil || !m.mouseCaptureActive {
		return false
	}
	return m.ownedScreen && m.overlay == nil && m.modal == nil && m.agentsOverview == nil
}

// linkHoverSGR is the foreground painted over the hovered link. The theme
// accent keeps it consistent with the rest of the TUI; a monochrome terminal
// falls back to a bright ANSI colour.
func (m *Model) linkHoverSGR() string {
	if m != nil {
		if sgr := codextui.ThemeAccentSGR(m.activeTUITheme(), codextui.DetectStdoutColorLevel()); sgr != "" {
			return sgr
		}
	}
	return "\x1b[1;36m"
}

// applyLinkHover recolours the OSC 8 link painted under the pointer. It runs on
// every draw, so scrolling, resizing or new output cannot leave the highlight
// on stale content (Rust #62390 recomputes the highlight on redraw).
func (m *Model) applyLinkHover(frame string) string {
	if m == nil || !m.hoverPointerSet || frame == "" || !m.linkHoverApplies() {
		return frame
	}
	rows := strings.Split(frame, "\n")
	if m.hoverPointerY < 0 || m.hoverPointerY >= len(rows) {
		return frame
	}
	span, ok := paintedLinkSpanAt(rows[m.hoverPointerY], m.hoverPointerX)
	if !ok {
		return frame
	}
	rows[m.hoverPointerY] = restylePaintedLink(rows[m.hoverPointerY], span, m.linkHoverSGR())
	return strings.Join(rows, "\n")
}
