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
	o.viewport.SetContent(o.content)
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
	o.viewport.SetContent(content)
	if follow {
		o.viewport.GotoBottom()
		return
	}
	o.viewport.SetYOffset(offset)
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
