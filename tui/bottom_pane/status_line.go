package bottompane

// Rust parity: codex-rs/tui/src/bottom_pane/status_line.rs. A status line owns
// either native spans or prepared extension UI-tree content and measures and
// paints itself inside the footer viewport. Native content keeps the existing
// span renderer; extension content reuses the prepared UI-tree layout so links,
// wrapping and overflow ellipses match the rest of the TUI.

import (
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui"
)

// StatusLineFocus mirrors Rust StatusLineFocus: an unfocused surface keeps the
// same layout but paints one dim, theme-free status row.
type StatusLineFocus int

const (
	StatusLineFocused StatusLineFocus = iota
	StatusLineUnfocused
)

// StatusLineRenderOptions carries the native span styling and the extension
// theme resolution used to paint rows.
type StatusLineRenderOptions struct {
	PullRequestURL string
	ThreadColorSGR string
	AccentColorSGR func(StatusLineAccent) string
	Bold           bool
	ThemeID        string
	ColorLevel     tui.StdoutColorLevel
}

// StatusLineRow is one painted status row. Styled keeps SGR/OSC-8 sequences;
// Plain is the same row with terminal control sequences removed.
type StatusLineRow struct {
	Styled string
	Plain  string
}

// NewStatusLineFromUITree prepares extension status content (Rust
// `impl From<UiTree> for StatusLine`). Unknown nodes fail closed just like
// ParseUITree, so an invalid provider response never reaches the footer.
func NewStatusLineFromUITree(node *tui.UINode) (StatusLine, bool) {
	if node == nil {
		return StatusLine{}, false
	}
	prepared := node.Prepare()
	return StatusLine{prepared: &prepared, layoutCache: &statusLineLayoutCache{}}, true
}

// StatusLineFromUITreeJSON decodes and prepares one extension status tree.
func StatusLineFromUITreeJSON(data []byte) (StatusLine, error) {
	node, err := tui.ParseUITree(data)
	if err != nil {
		return StatusLine{}, err
	}
	line, _ := NewStatusLineFromUITree(node)
	return line, nil
}

// IsExtension reports whether this line carries prepared UI-tree content.
func (l StatusLine) IsExtension() bool {
	return l.prepared != nil
}

// AsNative returns the native span content and whether the line is native.
func (l StatusLine) AsNative() (StatusLine, bool) {
	if l.prepared != nil {
		return StatusLine{}, false
	}
	return l, true
}

// statusLineLayoutCache mirrors Rust CachedLayout: the prepared tree's layout
// for one viewport is reused until the measured bounds change. Height and
// RenderRows share it, so a frame that reserves the status rows and then paints
// them lays the tree out once instead of once per call.
type statusLineLayoutCache struct {
	mu       sync.Mutex
	width    int
	height   int
	measured int
	rows     []tui.UILine
	valid    bool
}

func (c *statusLineLayoutCache) layout(prepared *tui.PreparedUITree, width int, height int) ([]tui.UILine, int) {
	if prepared == nil || width <= 0 || height <= 0 {
		return nil, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && c.width == width && c.height == height {
		return c.rows, c.measured
	}
	rows := prepared.RenderLines(width, height)
	c.width, c.height, c.rows, c.measured, c.valid = width, height, rows, len(rows), true
	return rows, c.measured
}

// extensionLayout returns the cached rows and measured height for extension
// content. Native content never reaches the cache.
func (l StatusLine) extensionLayout(width int, height int) ([]tui.UILine, int) {
	if l.prepared == nil || l.layoutCache == nil {
		return nil, 0
	}
	return l.layoutCache.layout(l.prepared, width, height)
}

// Height measures the rows this status line wants inside bounds. Rust clamps
// native content to one row when the caller offers a single row and otherwise
// reports the wrapped row count; extension content reports the UI-tree layout
// height at the supplied width.
func (l StatusLine) Height(width int, height int) int {
	if width <= 0 || height <= 0 {
		return 0
	}
	if l.prepared != nil {
		_, measured := l.extensionLayout(width, height)
		return measured
	}
	if len(l.Spans) == 0 {
		return 0
	}
	if tui.DisplayWidth(l.PlainText()) <= width || height <= 1 {
		return 1
	}
	rows := WrapStyledStatusRows(l.RenderStyled("", nil, false), width, height)
	return max(len(rows), 1)
}

// RenderRows paints the status line inside a width x height viewport. Native
// rows reuse the span renderer and wrap as styled text. Extension rows use
// PreparedUITree.RenderLines, which keeps OSC-8 links on painted cells and
// places overflow ellipses outside any link.
func (l StatusLine) RenderRows(width int, height int, focus StatusLineFocus, options StatusLineRenderOptions) []StatusLineRow {
	if width <= 0 || height <= 0 {
		return nil
	}
	if l.prepared != nil {
		lines, _ := l.extensionLayout(width, height)
		if len(lines) == 0 {
			return nil
		}
		rows := make([]StatusLineRow, 0, len(lines))
		for _, line := range lines {
			if focus == StatusLineUnfocused {
				rows = append(rows, StatusLineRow{
					Styled: dimStatusText(line.Text),
					Plain:  line.Text,
				})
				continue
			}
			rows = append(rows, StatusLineRow{
				Styled: line.RenderStyled(options.ThemeID, options.ColorLevel),
				Plain:  line.Text,
			})
		}
		return rows
	}
	if len(l.Spans) == 0 {
		return nil
	}
	styled := ""
	if focus == StatusLineUnfocused {
		styled = dimStatusText(l.PlainText())
	} else {
		styled = l.RenderStyledWithHyperlinks(options.PullRequestURL, options.ThreadColorSGR, options.AccentColorSGR, options.Bold)
	}
	wrapped := WrapStyledStatusRows(styled, width, height)
	rows := make([]StatusLineRow, 0, len(wrapped))
	for _, row := range wrapped {
		rows = append(rows, StatusLineRow{Styled: row, Plain: ansi.Strip(row)})
	}
	return rows
}

// WrapStyledStatusRows wraps ANSI-styled text at the full available width and
// caps the result at maxRows. A clipped final row carries an ellipsis so the
// truncation stays visible. This is the shared native status renderer for the
// live footer and the row-budget helpers.
func WrapStyledStatusRows(status string, width int, maxRows int) []string {
	if strings.TrimSpace(ansi.Strip(status)) == "" || width < 1 || maxRows < 1 {
		return nil
	}
	var rows []string
	for _, line := range strings.Split(status, "\n") {
		rows = append(rows, strings.Split(ansi.Wrap(line, width, ""), "\n")...)
	}
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		last := rows[maxRows-1]
		trimmed := ansi.Truncate(last, max(width-1, 0), "")
		rows[maxRows-1] = trimmed + "\u2026"
	}
	return rows
}

// dimStatusText matches Rust's Style::reset().dim() plain status style: all
// theme colours and hyperlinks are suppressed while the layout is unchanged.
func dimStatusText(text string) string {
	if text == "" {
		return ""
	}
	return statusLineResetSGR + statusLineDimSGR + text + statusLineResetSGR
}
