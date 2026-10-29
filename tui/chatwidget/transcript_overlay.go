package chatwidget

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	codextui "codex_go/tui"
	"codex_go/utils"
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
	PagerFind         = "find"

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
	// navigationHint/actionsHint are the pager's two help rows (Rust
	// pager_overlay::transcript::render_hints). The owner resolves key bindings.
	navigationHint string
	actionsHint    string
	// highlightStart/highlightEnd bound the 0-based, end-exclusive content line
	// range drawn in reverse video (Rust PagerView::set_highlight_cell).
	highlightStart int
	highlightEnd   int
	hasHighlight   bool
	// mouseScroll mirrors Rust #50209: the transcript pager applies
	// `tui.mouse_scroll_speed` to each wheel event and keeps the fractional
	// remainder for the next event.
	mouseScroll codextui.MouseScrollAccumulator
	// find is the Ctrl+T pager's find bar (Rust transcript_view::search).
	find transcriptFind
	// plainLines caches the ANSI-stripped content rows used by keyboard and
	// mouse selection (#57221-#57224).
	plainLines []string
	// selectMode is Rust transcript_view::SelectMode over this pager's rows.
	selectMode selectModeState
	// dragActive keeps a mouse drag extending the current selection.
	dragActive bool
	// lastClick/lastClickAt track the multi-click gesture so double-click marks a
	// word and triple-click marks a row (Rust text_selection::click_count).
	lastClickAt    time.Time
	lastClickCol   int
	lastClickRow   int
	lastClickCount int
}

func NewTranscriptOverlay(width int, height int, content string) *TranscriptOverlay {
	return NewTranscriptOverlayWithTitle(width, height, content, "T R A N S C R I P T")
}

func NewTranscriptOverlayWithTitle(width int, height int, content string, title string) *TranscriptOverlay {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "T R A N S C R I P T"
	}
	overlay := &TranscriptOverlay{
		title:          title,
		navigationHint: "↑/↓ to scroll · pgup/pgdn to page · home/end to jump",
		actionsHint:    "q close · f3 find · esc browse prompts",
		mouseScroll:    codextui.NewMouseScrollAccumulator(nil),
	}
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
	bodyHeight := height - 3
	if bodyHeight < minOverlayBodyHeight {
		bodyHeight = minOverlayBodyHeight
	}

	follow := o.content == "" || o.viewport.AtBottom()
	offset := o.viewport.YOffset
	if o.viewport.Width <= 0 {
		o.viewport = viewport.New(width, bodyHeight)
		// Rust #50209: the wheel is handled by the overlay itself so the
		// configurable speed (and its fractional accumulation) applies; the
		// viewport's fixed three-row wheel handling stays off.
		o.viewport.MouseWheelEnabled = false
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

// SetHints installs the resolved two-row pager help text.
func (o *TranscriptOverlay) SetHints(navigation string, actions string) {
	if o == nil {
		return
	}
	o.navigationHint = strings.TrimSpace(navigation)
	o.actionsHint = strings.TrimSpace(actions)
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
	o.refreshPlainLines()
	o.viewport.SetContent(o.viewContent())
	if follow {
		o.viewport.GotoBottom()
		if o.find.active() {
			o.refreshFindMatches()
		}
		return
	}
	o.viewport.SetYOffset(offset)
	if o.find.active() {
		o.refreshFindMatches()
	}
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
	if o == nil || o.content == "" {
		return o.content
	}
	_, hasFindMatch := o.find.matchLine()
	selecting := o.selectMode.active && o.selectMode.marked
	if !o.hasHighlight && !hasFindMatch && !selecting && !o.selectMode.active {
		return o.content
	}
	lines := strings.Split(o.content, "\n")
	if o.hasHighlight && o.highlightStart < len(lines) {
		end := o.highlightEnd
		if end > len(lines) {
			end = len(lines)
		}
		for index := o.highlightStart; index < end; index++ {
			lines[index] = reverseVideoLine(lines[index])
		}
	}
	// Rust transcript_view::search reverses the matched rows; this pager has one
	// line per match, so the matched line is the row it reverses (§T13).
	if line, ok := o.find.matchLine(); ok && line < len(lines) {
		lines[line] = reverseVideoLine(lines[line])
	}
	if o.selectMode.active {
		// Rust select_mode highlights the marked cells and underlines the caret.
		for row := range lines {
			if from, to, ok := o.selectionSpanForRow(row); ok {
				lines[row] = applyCellStyle(lines[row], from, to, selectionOn, selectionOff)
			}
		}
		cursor := o.selectCursor()
		if cursor.Row >= 0 && cursor.Row < len(lines) {
			if cursor.Col < o.lineWidth(cursor.Row) {
				lines[cursor.Row] = applyCellStyle(lines[cursor.Row], cursor.Col, cursor.Col+1, cursorOn, cursorOff)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// refreshPlainLines caches the ANSI-stripped rows selection operates on.
func (o *TranscriptOverlay) refreshPlainLines() {
	if o == nil {
		return
	}
	rows := strings.Split(o.content, "\n")
	plain := make([]string, len(rows))
	for index, row := range rows {
		plain[index] = utils.StripANSI(row)
	}
	o.plainLines = plain
}

// applyCellStyle wraps the display cells [from, to) of one rendered row in the
// given SGR attributes. Escape sequences are copied through untouched, and a
// full reset while the range is still open re-asserts the attribute.
func applyCellStyle(line string, from int, to int, on string, off string) string {
	if from >= to {
		return line
	}
	var builder strings.Builder
	builder.Grow(len(line) + len(on) + len(off))
	column := 0
	styled := false
	closed := false
	for index := 0; index < len(line); {
		if line[index] == 0x1b {
			next, sequence := ansiSequence(line[index:])
			builder.WriteString(sequence)
			if styled && (sequence == "\x1b[0m" || sequence == "\x1b[m") {
				builder.WriteString(on)
			}
			index += next
			continue
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		width := runewidth.RuneWidth(r)
		if width == 0 {
			width = 1
		}
		if !styled && column >= from {
			builder.WriteString(on)
			styled = true
		}
		if styled && column >= to && !closed {
			builder.WriteString(off)
			closed = true
		}
		builder.WriteString(line[index : index+size])
		column += width
		index += size
	}
	if styled && !closed {
		builder.WriteString(off)
	}
	return builder.String()
}

// ansiSequence returns the length and text of the escape sequence at the start
// of value, which must begin with ESC.
func ansiSequence(value string) (int, string) {
	if len(value) < 2 {
		return len(value), value
	}
	switch value[1] {
	case '[':
		index := 2
		for index < len(value) && !(value[index] >= 0x40 && value[index] <= 0x7e) {
			index++
		}
		if index < len(value) {
			index++
		}
		if index > len(value) {
			index = len(value)
		}
		return index, value[:index]
	case ']':
		index := 2
		for index < len(value) {
			if value[index] == 0x07 {
				index++
				break
			}
			if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
				index += 2
				break
			}
			index++
		}
		return index, value[:index]
	default:
		return 2, value[:2]
	}
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
		o.viewport.ScrollUp(o.halfPageRows())
	case PagerHalfPageDown:
		o.viewport.ScrollDown(o.halfPageRows())
	case PagerJumpTop:
		o.viewport.GotoTop()
	case PagerJumpBottom:
		o.viewport.GotoBottom()
	default:
		return false
	}
	return true
}

// halfPageRows mirrors Rust #43889: the pager rounds a half page up against the
// rendered content height. Go's viewport height is the last rendered body
// height, while the viewport library's own HalfPage helpers round down.
func (o *TranscriptOverlay) halfPageRows() int {
	if o == nil || o.viewport.Height <= 0 {
		return 0
	}
	return (o.viewport.Height + 1) / 2
}

// SetMouseScrollSpeed applies the resolved `tui.mouse_scroll_speed` (Rust #50209
// threads it into `TranscriptOverlay::new` from the local settings).
func (o *TranscriptOverlay) SetMouseScrollSpeed(speed float64) {
	if o == nil {
		return
	}
	o.mouseScroll.SetSpeed(speed)
}

// MouseScrollSpeed reports the live multiplier.
func (o *TranscriptOverlay) MouseScrollSpeed() float64 {
	if o == nil {
		return codextui.MouseScrollSpeedDefault
	}
	return o.mouseScroll.Speed()
}

// wheelRows maps one wheel event to the whole rows the pager should move,
// mirroring Rust `TranscriptView::handle_mouse` (#50209): up scrolls toward
// earlier output (negative rows), down toward later output.
func (o *TranscriptOverlay) wheelRows(msg bubbletea.MouseMsg) (int, bool) {
	if o == nil || msg.Action != bubbletea.MouseActionPress {
		return 0, false
	}
	switch msg.Button {
	case bubbletea.MouseButtonWheelUp:
		return o.mouseScroll.Rows(-1), true
	case bubbletea.MouseButtonWheelDown:
		return o.mouseScroll.Rows(1), true
	default:
		return 0, false
	}
}

func (o *TranscriptOverlay) Update(message bubbletea.Msg) bubbletea.Cmd {
	if o == nil {
		return nil
	}
	if msg, ok := message.(bubbletea.MouseMsg); ok {
		if rows, wheel := o.wheelRows(msg); wheel {
			// Rust #50209 applies the accumulated rows to the transcript view;
			// the pager's follow behavior is derived from the new offset.
			if rows < 0 {
				o.viewport.ScrollUp(-rows)
			} else if rows > 0 {
				o.viewport.ScrollDown(rows)
			}
			return nil
		}
		if o.handleSelectMouse(msg) {
			return nil
		}
	}
	var cmd bubbletea.Cmd
	o.viewport, cmd = o.viewport.Update(message)
	return cmd
}

// handleSelectMouse maps left-button drags onto the row/column selection model
// (Rust transcript_view::input mouse selection). The overlay header occupies
// screen row 0, so body rows are offset by one.
func (o *TranscriptOverlay) handleSelectMouse(msg bubbletea.MouseMsg) bool {
	if o == nil {
		return false
	}
	switch msg.Action {
	case bubbletea.MouseActionPress:
		if msg.Button != bubbletea.MouseButtonLeft {
			return false
		}
		point := o.mouseSelectPoint(msg)
		// Rust #47414: Shift-click extends the existing range from its original
		// anchor and unit instead of starting a new gesture.
		if msg.Shift && o.hasSelectionRange() {
			o.lastClickAt = time.Time{}
			o.extendMouseSelection(point)
			o.dragActive = true
			o.viewport.SetContent(o.viewContent())
			return true
		}
		clicks := o.mouseClickCount(msg)
		if clicks >= 2 && point.Col >= o.lineWidth(point.Row) {
			// Rust ignores repeated clicks past the end of a row so empty space
			// never selects a word or line.
			return false
		}
		if !o.selectMode.active {
			o.selectMode = selectModeState{
				active:          true,
				preferredColumn: -1,
				savedOffset:     o.viewport.YOffset,
				savedFollow:     o.viewport.AtBottom(),
			}
		}
		unit := selectUnitForClicks(clicks)
		start, end := o.unitRangeAt(unit, point)
		o.selectMode.marked = start != end
		o.selectMode.unit = unit
		o.selectMode.anchor = start
		o.selectMode.activeEnd = end
		o.selectMode.originStart = start
		o.selectMode.originEnd = end
		o.selectMode.preferredColumn = -1
		o.dragActive = true
		o.viewport.SetContent(o.viewContent())
		return true
	case bubbletea.MouseActionMotion:
		if !o.dragActive {
			return false
		}
		point := o.mouseSelectPoint(msg)
		o.extendMouseSelection(point)
		o.viewport.SetContent(o.viewContent())
		return true
	case bubbletea.MouseActionRelease:
		if !o.dragActive {
			return false
		}
		o.dragActive = false
		return true
	}
	return false
}

// mouseClickCount counts consecutive presses at the same cell within the
// gesture window, cycling 1, 2, 3 like Rust `text_selection::click_count`.
func (o *TranscriptOverlay) mouseClickCount(msg bubbletea.MouseMsg) int {
	now := time.Now()
	clicks := 1
	if !o.lastClickAt.IsZero() && now.Sub(o.lastClickAt) < 400*time.Millisecond &&
		o.lastClickCol == msg.X && o.lastClickRow == msg.Y {
		clicks = o.lastClickCount%3 + 1
	}
	o.lastClickAt = now
	o.lastClickCol = msg.X
	o.lastClickRow = msg.Y
	o.lastClickCount = clicks
	return clicks
}

// extendMouseSelection moves the active end to point while keeping the gesture's
// original unit range as the fixed side of the selection, so repeated Shift
// clicks and continued dragging can extend it in either direction (#47414).
func (o *TranscriptOverlay) extendMouseSelection(point selectPoint) {
	if !o.selectMode.active {
		return
	}
	start, end := o.unitRangeAt(o.selectMode.unit, point)
	origin := o.selectMode.originStart
	if start.before(origin) {
		o.selectMode.anchor = o.selectMode.originEnd
		o.selectMode.activeEnd = start
	} else {
		o.selectMode.anchor = o.selectMode.originStart
		o.selectMode.activeEnd = end
	}
	o.selectMode.marked = o.selectMode.anchor != o.selectMode.activeEnd
}

func (o *TranscriptOverlay) mouseSelectPoint(msg bubbletea.MouseMsg) selectPoint {
	row := o.viewport.YOffset + msg.Y - 1
	if row < 0 {
		row = 0
	}
	if row >= len(o.plainLines) {
		row = len(o.plainLines) - 1
	}
	column := msg.X
	if column < 0 {
		column = 0
	}
	if row >= 0 {
		if width := o.lineWidth(row); column > width {
			column = width
		}
	}
	return selectPoint{Row: row, Col: column}
}

func (o *TranscriptOverlay) View() string {
	if o == nil {
		return ""
	}
	width := firstPositive(o.width, defaultOverlayWidth)
	header := overlayHeader(width, o.scrollPercent(), o.title)
	help := fitLine(o.navigationHint, width)
	actions := fitLine(o.actionsHint, width)
	if o.find.active() {
		actions = fitLine(o.FindBarLine(), width)
	}
	return strings.Join([]string{header, o.viewport.View(), help, actions}, "\n")
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
