package chatwidget

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
)

const (
	PagerScrollUp     = "scroll_up"
	PagerScrollDown   = "scroll_down"
	PagerPageUp       = "page_up"
	PagerPageDown     = "page_down"
	PagerHalfPageUp   = "half_page_up"
	PagerHalfPageDown = "half_page_down"
	PagerJumpTop      = "jump_top"
	PagerJumpBottom   = "jump_bottom"

	defaultOverlayWidth  = 80
	defaultOverlayHeight = 24
	minOverlayBodyHeight = 1

	reverseVideoOn  = "\x1b[7m"
	reverseVideoOff = "\x1b[27m"
)

// TranscriptOverlay mirrors Rust chatwidget's Ctrl+T transcript pager: a
// fullscreen transcript view whose scroll position follows the tail only when
// already at the bottom.
type TranscriptOverlay struct {
	viewport viewport.Model
	width    int
	height   int
	content  string
	title    string
	// highlightStart/highlightEnd bound the 0-based, end-exclusive content line
	// range drawn in reverse video (Rust PagerView::set_highlight_cell).
	highlightStart int
	highlightEnd   int
	hasHighlight   bool
}

func NewTranscriptOverlay(width int, height int, content string) *TranscriptOverlay {
	return NewTranscriptOverlayWithTitle(width, height, content, "T R A N S C R I P T")
}

func NewTranscriptOverlayWithTitle(width int, height int, content string, title string) *TranscriptOverlay {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "T R A N S C R I P T"
	}
	overlay := &TranscriptOverlay{title: title}
	overlay.Resize(width, height)
	overlay.SetContent(content)
	overlay.viewport.GotoBottom()
	return overlay
}

func (o *TranscriptOverlay) Resize(width int, height int) {
	if o == nil {
		return
	}
	width = firstPositive(width, defaultOverlayWidth)
	height = firstPositive(height, defaultOverlayHeight)
	// Avoid re-laying-out the viewport (and re-wrapping the content) on every
	// frame when the terminal size is unchanged. Content changes are delivered
	// separately through SetContent, which preserves the scroll position.
	if o.viewport.Width > 0 && o.width == width && o.height == height {
		return
	}
	bodyHeight := height - 2
	if bodyHeight < minOverlayBodyHeight {
		bodyHeight = minOverlayBodyHeight
	}

	follow := o.content == "" || o.viewport.AtBottom()
	offset := o.viewport.YOffset
	if o.viewport.Width <= 0 {
		o.viewport = viewport.New(width, bodyHeight)
		o.viewport.MouseWheelEnabled = true
		o.viewport.MouseWheelDelta = 3
	} else {
		o.viewport.Width = width
		o.viewport.Height = bodyHeight
	}
	o.width = width
	o.height = height
	o.viewport.SetContent(o.viewContent())
	if follow {
		o.viewport.GotoBottom()
		return
	}
	o.viewport.SetYOffset(offset)
}

func (o *TranscriptOverlay) SetContent(content string) {
	if o == nil {
		return
	}
	content = strings.TrimRight(content, "\r\n")
	if strings.TrimSpace(content) == "" {
		content = "No messages yet."
	}
	follow := o.content == "" || o.viewport.AtBottom()
	offset := o.viewport.YOffset
	o.content = content
	o.viewport.SetContent(o.viewContent())
	if follow {
		o.viewport.GotoBottom()
		return
	}
	o.viewport.SetYOffset(offset)
}

// SetHighlightRange highlights content lines [start, end) (0-based, end
// exclusive) in reverse video, mirroring the selected user message in Rust's
// backtrack preview. An empty or inverted range clears the highlight.
func (o *TranscriptOverlay) SetHighlightRange(start int, end int) {
	if o == nil {
		return
	}
	if start < 0 || end <= start {
		o.ClearHighlightRange()
		return
	}
	if o.hasHighlight && o.highlightStart == start && o.highlightEnd == end {
		return
	}
	o.highlightStart = start
	o.highlightEnd = end
	o.hasHighlight = true
	o.viewport.SetContent(o.viewContent())
	o.scrollToHighlight()
}

// ClearHighlightRange removes any highlight and restores the plain content.
func (o *TranscriptOverlay) ClearHighlightRange() {
	if o == nil || !o.hasHighlight {
		return
	}
	o.hasHighlight = false
	o.highlightStart = 0
	o.highlightEnd = 0
	o.viewport.SetContent(o.viewContent())
}

// HighlightRange reports the active highlight range, if any.
func (o *TranscriptOverlay) HighlightRange() (int, int, bool) {
	if o == nil || !o.hasHighlight {
		return 0, 0, false
	}
	return o.highlightStart, o.highlightEnd, true
}

// viewContent is the content the viewport renders: the base content, with the
// highlighted lines wrapped in reverse video when a highlight is active.
func (o *TranscriptOverlay) viewContent() string {
	if o == nil || !o.hasHighlight || o.content == "" {
		return o.content
	}
	lines := strings.Split(o.content, "\n")
	if o.highlightStart >= len(lines) {
		return o.content
	}
	end := o.highlightEnd
	if end > len(lines) {
		end = len(lines)
	}
	for index := o.highlightStart; index < end; index++ {
		lines[index] = reverseVideoLine(lines[index])
	}
	return strings.Join(lines, "\n")
}

// scrollToHighlight keeps the selected message on screen without snapping the
// viewport to the top when it is already visible.
func (o *TranscriptOverlay) scrollToHighlight() {
	if o == nil || !o.hasHighlight {
		return
	}
	if o.highlightStart < o.viewport.YOffset {
		o.viewport.SetYOffset(o.highlightStart)
		return
	}
	bodyHeight := o.viewport.Height
	if bodyHeight <= 0 {
		return
	}
	if o.highlightEnd > o.viewport.YOffset+bodyHeight {
		offset := o.highlightEnd - bodyHeight
		if offset < 0 {
			offset = 0
		}
		o.viewport.SetYOffset(offset)
	}
}

// reverseVideoLine wraps one rendered line in reverse video, re-asserting the
// attribute after every full SGR reset so an embedded color cannot cancel the
// highlight mid-line (Rust applies the reversed style to the whole cell).
func reverseVideoLine(line string) string {
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+reverseVideoOn)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+reverseVideoOn)
	return reverseVideoOn + line + reverseVideoOff
}

func (o *TranscriptOverlay) Content() string {
	if o == nil {
		return ""
	}
	return o.content
}

func (o *TranscriptOverlay) YOffset() int {
	if o == nil {
		return 0
	}
	return o.viewport.YOffset
}

func (o *TranscriptOverlay) AtTop() bool {
	return o == nil || o.viewport.AtTop()
}

func (o *TranscriptOverlay) AtBottom() bool {
	return o == nil || o.viewport.AtBottom()
}

func (o *TranscriptOverlay) ApplyPagerAction(action string) bool {
	if o == nil {
		return false
	}
	switch action {
	case PagerScrollUp:
		o.viewport.LineUp(1)
	case PagerScrollDown:
		o.viewport.LineDown(1)
	case PagerPageUp:
		o.viewport.PageUp()
	case PagerPageDown:
		o.viewport.PageDown()
	case PagerHalfPageUp:
		o.viewport.HalfPageUp()
	case PagerHalfPageDown:
		o.viewport.HalfPageDown()
	case PagerJumpTop:
		o.viewport.GotoTop()
	case PagerJumpBottom:
		o.viewport.GotoBottom()
	default:
		return false
	}
	return true
}

func (o *TranscriptOverlay) Update(message bubbletea.Msg) bubbletea.Cmd {
	if o == nil {
		return nil
	}
	var cmd bubbletea.Cmd
	o.viewport, cmd = o.viewport.Update(message)
	return cmd
}

func (o *TranscriptOverlay) View() string {
	if o == nil {
		return ""
	}
	width := firstPositive(o.width, defaultOverlayWidth)
	header := overlayHeader(width, o.scrollPercent(), o.title)
	help := fitLine("up/down/k/j scroll | pgup/pgdn page | home/end jump | q/ctrl+t close", width)
	return strings.Join([]string{header, o.viewport.View(), help}, "\n")
}

func (o *TranscriptOverlay) scrollPercent() int {
	if o == nil {
		return 100
	}
	percent := o.viewport.ScrollPercent()
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return 100
	}
	value := int(math.Round(percent * 100))
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func overlayHeader(width int, percent int, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "T R A N S C R I P T"
	}
	right := fmt.Sprintf("%3d%%", percent)
	if width <= 0 {
		return title + " " + right
	}
	if width <= len(title)+len(right)+1 {
		return fitLine(title+" "+right, width)
	}
	return title + strings.Repeat(" ", width-len(title)-len(right)) + right
}

func fitLine(line string, width int) string {
	line = strings.ReplaceAll(line, "\r", " ")
	line = strings.ReplaceAll(line, "\n", " ")
	if width <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func firstPositive(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
