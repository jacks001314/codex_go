package tea

import (
	keymsg "codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
	"strings"

	bubbleskey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// transcriptViewport holds the transcript pane's scroll state over rows the
// transcript renderer already produced.
//
// Rust's TranscriptView keeps each cell's layout and paints only the visible
// rows, so a frame costs one screenful of work no matter how long the thread
// is. bubbles/viewport instead re-splits the whole transcript and measures
// every row on each SetContent call, which is the dominant per-frame cost of a
// long session. This type keeps the viewport's scrolling semantics but accepts
// the rendered rows, so View only joins the rows it paints.
type transcriptViewport struct {
	Width   int
	Height  int
	YOffset int

	lines []string

	keyMap            viewport.KeyMap
	MouseWheelEnabled bool
	MouseWheelDelta   int
}

func newTranscriptViewport(width int, height int) transcriptViewport {
	return transcriptViewport{
		Width:           width,
		Height:          height,
		keyMap:          viewport.DefaultKeyMap(),
		MouseWheelDelta: 1,
	}
}

// SetLines replaces the rendered rows. Only the visible window is painted, so
// handing over the rows is O(1) apart from clamping the scroll offset.
func (v *transcriptViewport) SetLines(lines []string) {
	v.lines = lines
	if v.YOffset > v.maxYOffset() {
		v.YOffset = v.maxYOffset()
	}
}

// SetContent keeps the plain-text entry point for callers (and tests) that only
// have a joined string.
func (v *transcriptViewport) SetContent(content string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	v.SetLines(strings.Split(content, "\n"))
}

func (v *transcriptViewport) maxYOffset() int {
	return max(0, len(v.lines)-v.Height)
}

// AtBottom reports whether the view is following the newest row.
func (v *transcriptViewport) AtBottom() bool { return v.YOffset >= v.maxYOffset() }

// AtTop reports whether the view is at the oldest row.
func (v *transcriptViewport) AtTop() bool { return v.YOffset <= 0 }

// GotoBottom follows the newest row.
func (v *transcriptViewport) GotoBottom() { v.YOffset = v.maxYOffset() }

// GotoTop jumps to the oldest row.
func (v *transcriptViewport) GotoTop() { v.YOffset = 0 }

// SetYOffset clamps the first painted row.
func (v *transcriptViewport) SetYOffset(offset int) {
	v.YOffset = clampViewportOffset(offset, 0, v.maxYOffset())
}

// ScrollUp moves the view up by n rows.
func (v *transcriptViewport) ScrollUp(n int) {
	if v.AtTop() || n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.YOffset - n)
}

// ScrollDown moves the view down by n rows.
func (v *transcriptViewport) ScrollDown(n int) {
	if v.AtBottom() || n == 0 || len(v.lines) == 0 {
		return
	}
	v.SetYOffset(v.YOffset + n)
}

func (v *transcriptViewport) pageDown() {
	if v.AtBottom() {
		return
	}
	v.ScrollDown(v.Height)
}

func (v *transcriptViewport) pageUp() {
	if v.AtTop() {
		return
	}
	v.ScrollUp(v.Height)
}

func (v *transcriptViewport) halfPageDown() {
	if v.AtBottom() {
		return
	}
	v.ScrollDown(v.Height / 2)
}

func (v *transcriptViewport) halfPageUp() {
	if v.AtTop() {
		return
	}
	v.ScrollUp(v.Height / 2)
}

// View paints exactly the visible window, padded to the pane like the viewport
// it replaces so the surrounding layout math stays unchanged.
func (v transcriptViewport) View() string {
	if v.Width <= 0 || v.Height <= 0 {
		return ""
	}
	top := max(0, v.YOffset)
	bottom := clampViewportOffset(v.YOffset+v.Height, top, len(v.lines))
	content := ""
	if top < bottom {
		// Cut each painted row to the pane width first: lipgloss Width wraps
		// overflowing rows onto extra lines, while the terminal pane clips them.
		visible := make([]string, bottom-top)
		for index, line := range v.lines[top:bottom] {
			visible[index] = ansi.Cut(line, 0, v.Width)
		}
		content = strings.Join(visible, "\n")
	}
	return lipgloss.NewStyle().
		Width(v.Width).
		Height(v.Height).
		MaxHeight(v.Height).
		MaxWidth(v.Width).
		Render(content)
}

// Update keeps the viewport's key and wheel bindings.
func (v transcriptViewport) Update(msg bubbletea.Msg) (transcriptViewport, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case keymsg.KeyMsg:
		switch {
		case bubbleskey.Matches(msg, v.keyMap.PageDown):
			v.pageDown()
		case bubbleskey.Matches(msg, v.keyMap.PageUp):
			v.pageUp()
		case bubbleskey.Matches(msg, v.keyMap.HalfPageDown):
			v.halfPageDown()
		case bubbleskey.Matches(msg, v.keyMap.HalfPageUp):
			v.halfPageUp()
		case bubbleskey.Matches(msg, v.keyMap.Down):
			v.ScrollDown(1)
		case bubbleskey.Matches(msg, v.keyMap.Up):
			v.ScrollUp(1)
		}
	case mousemsg.MouseMsg:
		if !v.MouseWheelEnabled || msg.Action != mousemsg.MouseActionPress {
			break
		}
		switch msg.Button {
		case mousemsg.MouseButtonWheelUp:
			v.ScrollUp(v.MouseWheelDelta)
		case mousemsg.MouseButtonWheelDown:
			v.ScrollDown(v.MouseWheelDelta)
		}
	}
	return v, nil
}

func clampViewportOffset(value int, low int, high int) int {
	if high < low {
		return low
	}
	return max(low, min(value, high))
}
