package chatwidget

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Rust parity: codex-rs/tui/src/transcript_view/select_mode.rs and
// select_mode/motion.rs (`SelectMode`, `CursorMotion`) plus the copy/follow
// actions in pager_overlay/transcript.rs (#57221-#57224).
//
// Rust tracks the cursor as an `Anchor` into a per-cell text layout. This pager
// keeps the transcript as one rendered line per entry row, so the same motions
// run over (row, display-column) pairs instead.

// SelectUnit is the granularity used by a marked range.
type SelectUnit int

const (
	// SelectUnitCharacter marks individual grapheme positions.
	SelectUnitCharacter SelectUnit = iota
	// SelectUnitLine marks whole logical rows (`V`).
	SelectUnitLine
	// SelectUnitWord marks the word under a double-click (Rust #47414).
	SelectUnitWord
)

// SelectAction reports what the key handler asks the owner to do next.
type SelectAction int

const (
	// SelectActionNone consumed the key without a visible change.
	SelectActionNone SelectAction = iota
	// SelectActionChanged re-renders the overlay.
	SelectActionChanged
	// SelectActionCopy copies the marked range and keeps selection mode open.
	SelectActionCopy
	// SelectActionCopyAndFollow copies the marked range, jumps to the latest
	// output, and closes the overlay.
	SelectActionCopyAndFollow
	// SelectActionExit leaves selection mode and closes the overlay.
	SelectActionExit
	// SelectActionBeginFind opens the find bar from selection mode.
	SelectActionBeginFind
)

// SelectCommand is one normalized selection control resolved by the owner's
// keymap. Keeping keymap lookup in the owner lets this package stay free of
// Bubble Tea key handling.
type SelectCommand int

const (
	SelectCmdNone SelectCommand = iota
	SelectCmdLeft
	SelectCmdRight
	SelectCmdUp
	SelectCmdDown
	SelectCmdWordForward
	SelectCmdWordBackward
	SelectCmdWordEnd
	SelectCmdLineStart
	SelectCmdLineEnd
	SelectCmdBeginning
	SelectCmdEnd
	SelectCmdPageUp
	SelectCmdPageDown
	SelectCmdHalfPageUp
	SelectCmdHalfPageDown
	SelectCmdMarkCharacter
	SelectCmdMarkLine
	SelectCmdSwapEnds
	SelectCmdCopy
	SelectCmdCopyAndFollow
	SelectCmdClearMark
	SelectCmdExit
	SelectCmdBeginFind
	SelectCmdRepeatFindOlder
	SelectCmdRepeatFindNewer
)

const (
	selectionOn  = "\x1b[7m"
	selectionOff = "\x1b[27m"
	cursorOn     = "\x1b[4m"
	cursorOff    = "\x1b[24m"
)

type selectPoint struct {
	Row int
	Col int
}

func (p selectPoint) before(other selectPoint) bool {
	if p.Row != other.Row {
		return p.Row < other.Row
	}
	return p.Col < other.Col
}

type selectOrigin struct {
	marked          bool
	unit            SelectUnit
	anchor          selectPoint
	active          selectPoint
	preferredColumn int
}

type selectModeState struct {
	active    bool
	marked    bool
	unit      SelectUnit
	anchor    selectPoint
	activeEnd selectPoint
	// originStart/originEnd remember the text unit a mouse gesture started on so
	// Shift-click can extend it in either direction without losing the anchor
	// (Rust transcript_view::Selection::origin, #47414).
	originStart     selectPoint
	originEnd       selectPoint
	preferredColumn int
	savedOffset     int
	savedFollow     bool
	searchOrigin    *selectOrigin
}

// SelectionActive reports whether keyboard selection owns the overlay input.
func (o *TranscriptOverlay) SelectionActive() bool {
	return o != nil && o.selectMode.active
}

// SelectionMarked reports whether a range is currently marked.
func (o *TranscriptOverlay) SelectionMarked() bool {
	return o != nil && o.selectMode.active && o.selectMode.marked
}

// SelectionCursor reports the caret position as a 0-based content row and
// display column.
func (o *TranscriptOverlay) SelectionCursor() (int, int) {
	if o == nil || !o.selectMode.active {
		return 0, 0
	}
	cursor := o.selectCursor()
	return cursor.Row, cursor.Col
}

// BeginSelection enters keyboard selection at the bottommost visible row, or at
// the accepted find match when one is on screen (Rust begin_select_mode).
func (o *TranscriptOverlay) BeginSelection() bool {
	if o == nil {
		return false
	}
	if o.selectMode.active {
		return true
	}
	if len(o.plainLines) == 0 {
		return false
	}
	cursor := o.selectionEntryCursor()
	o.selectMode = selectModeState{
		active:          true,
		anchor:          cursor,
		activeEnd:       cursor,
		preferredColumn: -1,
		savedOffset:     o.viewport.YOffset,
		savedFollow:     o.viewport.AtBottom(),
	}
	if line, ok := o.find.matchLine(); ok {
		o.selectMode.searchOrigin = &selectOrigin{}
		cursor = o.selectPointForMatch(line)
		o.selectMode.anchor = cursor
		o.selectMode.activeEnd = cursor
	}
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
	o.dragActive = false
	return true
}

// EndSelection leaves selection mode, restoring the reading position that was
// current when selection started.
func (o *TranscriptOverlay) EndSelection() {
	if o == nil || !o.selectMode.active {
		return
	}
	offset := o.selectMode.savedOffset
	follow := o.selectMode.savedFollow
	o.selectMode = selectModeState{preferredColumn: -1}
	o.viewport.SetContent(o.viewContent())
	if follow {
		o.viewport.GotoBottom()
		return
	}
	o.viewport.SetYOffset(offset)
}

// ClearSelectionMark drops the fixed anchor but keeps the caret in selection
// mode (Rust clear_select_mark). Escape with no mark exits selection instead.
func (o *TranscriptOverlay) ClearSelectionMark() SelectAction {
	if o == nil || !o.selectMode.active {
		return SelectActionNone
	}
	if !o.selectMode.marked {
		o.EndSelection()
		return SelectActionExit
	}
	cursor := o.selectCursor()
	o.selectMode.marked = false
	o.selectMode.anchor = cursor
	o.selectMode.activeEnd = cursor
	o.selectMode.preferredColumn = -1
	o.viewport.SetContent(o.viewContent())
	return SelectActionChanged
}

// selectionEntryCursor picks the bottommost visible selectable position, the
// same preference Rust uses when `search.match_anchor()` is unavailable.
func (o *TranscriptOverlay) selectionEntryCursor() selectPoint {
	if len(o.plainLines) == 0 {
		return selectPoint{}
	}
	row := o.viewport.YOffset + o.bodyHeight() - 1
	if row < 0 {
		row = 0
	}
	if row >= len(o.plainLines) {
		row = len(o.plainLines) - 1
	}
	if row < o.viewport.YOffset {
		row = o.viewport.YOffset
	}
	return selectPoint{Row: row, Col: 0}
}

func (o *TranscriptOverlay) selectPointForMatch(line int) selectPoint {
	if line < 0 || line >= len(o.plainLines) {
		return selectPoint{}
	}
	if o.find.query == "" {
		return selectPoint{Row: line}
	}
	index := strings.Index(strings.ToLower(o.plainLines[line]), strings.ToLower(o.find.query))
	if index < 0 {
		return selectPoint{Row: line}
	}
	return selectPoint{Row: line, Col: runewidth.StringWidth(o.plainLines[line][:index])}
}

func (o *TranscriptOverlay) bodyHeight() int {
	if o == nil || o.viewport.Height <= 0 {
		return 1
	}
	return o.viewport.Height
}

func (o *TranscriptOverlay) selectCursor() selectPoint {
	if o == nil {
		return selectPoint{}
	}
	return o.selectMode.activeEnd
}

// selectionRange returns the normalized display range. Whole-line marks expand
// both ends to the row boundaries.
func (o *TranscriptOverlay) selectionRange() (selectPoint, selectPoint) {
	start := o.selectMode.anchor
	end := o.selectMode.activeEnd
	if !o.selectMode.marked {
		return start, end
	}
	if end.before(start) {
		start, end = end, start
	}
	if o.selectMode.unit == SelectUnitLine {
		start.Col = 0
		end.Col = o.lineWidth(end.Row)
	}
	return start, end
}

func (o *TranscriptOverlay) lineWidth(row int) int {
	if row < 0 || row >= len(o.plainLines) {
		return 0
	}
	return runewidth.StringWidth(o.plainLines[row])
}

// hasSelectionRange reports whether a non-empty range is marked, matching Rust
// `TranscriptView::has_selection_range` (#47414).
func (o *TranscriptOverlay) hasSelectionRange() bool {
	if o == nil || !o.selectMode.active || !o.selectMode.marked {
		return false
	}
	start, end := o.selectionRange()
	return start != end
}

// selectUnitForClicks maps a click count onto a selection unit (Rust
// `SelectionUnit::from_clicks`).
func selectUnitForClicks(clicks int) SelectUnit {
	switch clicks {
	case 2:
		return SelectUnitWord
	case 3:
		return SelectUnitLine
	default:
		return SelectUnitCharacter
	}
}

// unitRangeAt returns the text unit covered at point. Character selections are
// a single position, word selections cover one word, and line selections cover
// the whole logical row (Rust `SelectionUnit::range`).
func (o *TranscriptOverlay) unitRangeAt(unit SelectUnit, point selectPoint) (selectPoint, selectPoint) {
	switch unit {
	case SelectUnitLine:
		return selectPoint{Row: point.Row, Col: 0}, selectPoint{Row: point.Row, Col: o.lineWidth(point.Row)}
	case SelectUnitWord:
		start, end := o.wordSpanAtColumn(point.Row, point.Col)
		return selectPoint{Row: point.Row, Col: start}, selectPoint{Row: point.Row, Col: end}
	default:
		return point, point
	}
}

// wordSpanAtColumn returns the display-column span of the word under column.
// Word runs and the non-word runs between them are separate units, mirroring
// `split_word_bound_indices` closely enough for pointer selection.
func (o *TranscriptOverlay) wordSpanAtColumn(row int, column int) (int, int) {
	if row < 0 || row >= len(o.plainLines) {
		return column, column
	}
	line := o.plainLines[row]
	runes := []rune(line)
	width := runewidth.StringWidth(line)
	if column >= width || len(runes) == 0 {
		return width, width
	}
	index := runeIndexAtColumn(line, column)
	if index >= len(runes) {
		return width, width
	}
	word := isWordRune(runes[index])
	start := index
	for start > 0 && isWordRune(runes[start-1]) == word {
		start--
	}
	end := index + 1
	for end < len(runes) && isWordRune(runes[end]) == word {
		end++
	}
	return runewidth.StringWidth(string(runes[:start])), runewidth.StringWidth(string(runes[:end]))
}

// SelectedText returns the plain text covered by the current mark.
func (o *TranscriptOverlay) SelectedText() string {
	if o == nil || !o.selectMode.active || !o.selectMode.marked {
		return ""
	}
	start, end := o.selectionRange()
	if start.Row == end.Row && start.Col == end.Col {
		return ""
	}
	lines := make([]string, 0, end.Row-start.Row+1)
	for row := start.Row; row <= end.Row; row++ {
		if row < 0 || row >= len(o.plainLines) {
			continue
		}
		line := o.plainLines[row]
		width := runewidth.StringWidth(line)
		from := 0
		to := width
		if row == start.Row {
			from = clampColumn(start.Col, width)
		}
		if row == end.Row {
			to = clampColumn(end.Col, width)
		}
		if from > to {
			from, to = to, from
		}
		lines = append(lines, sliceByDisplayColumns(line, from, to))
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t")
}

// HandleSelectCommand applies one selection control. It returns the action the
// owner should perform (re-render, copy, exit, or open find).
func (o *TranscriptOverlay) HandleSelectCommand(command SelectCommand) SelectAction {
	if o == nil || !o.selectMode.active {
		return SelectActionNone
	}
	switch command {
	case SelectCmdNone:
		return SelectActionNone
	case SelectCmdExit:
		o.EndSelection()
		o.cancelFindAfterSelect()
		return SelectActionExit
	case SelectCmdClearMark:
		action := o.ClearSelectionMark()
		if action == SelectActionExit {
			o.cancelFindAfterSelect()
		}
		return action
	case SelectCmdMarkCharacter:
		o.startSelectMark(SelectUnitCharacter)
		return SelectActionChanged
	case SelectCmdMarkLine:
		o.startSelectMark(SelectUnitLine)
		return SelectActionChanged
	case SelectCmdSwapEnds:
		o.swapSelectEnds()
		return SelectActionChanged
	case SelectCmdCopy:
		if strings.TrimSpace(o.SelectedText()) == "" {
			return SelectActionChanged
		}
		return SelectActionCopy
	case SelectCmdCopyAndFollow:
		if strings.TrimSpace(o.SelectedText()) == "" {
			o.EndSelection()
			o.cancelFindAfterSelect()
			return SelectActionExit
		}
		return SelectActionCopyAndFollow
	case SelectCmdBeginFind:
		o.beginSelectFind()
		o.refreshFindContent()
		return SelectActionBeginFind
	case SelectCmdRepeatFindOlder:
		if o.repeatSelectFind(false) {
			return SelectActionChanged
		}
		return SelectActionNone
	case SelectCmdRepeatFindNewer:
		if o.repeatSelectFind(true) {
			return SelectActionChanged
		}
		return SelectActionNone
	}
	moved := true
	switch command {
	case SelectCmdLeft:
		moved = o.moveCharacter(-1)
	case SelectCmdRight:
		moved = o.moveCharacter(1)
	case SelectCmdUp:
		moved = o.moveRows(-1)
	case SelectCmdDown:
		moved = o.moveRows(1)
	case SelectCmdPageUp:
		moved = o.moveRows(-max(o.bodyHeight()-1, 1))
	case SelectCmdPageDown:
		moved = o.moveRows(max(o.bodyHeight()-1, 1))
	case SelectCmdHalfPageUp:
		moved = o.moveRows(-((o.bodyHeight() + 1) / 2))
	case SelectCmdHalfPageDown:
		moved = o.moveRows((o.bodyHeight() + 1) / 2)
	case SelectCmdWordForward:
		moved = o.moveWord(false, false)
	case SelectCmdWordBackward:
		moved = o.moveWord(true, false)
	case SelectCmdWordEnd:
		moved = o.moveWord(false, true)
	case SelectCmdLineStart:
		moved = o.moveLineStart()
	case SelectCmdLineEnd:
		moved = o.moveLineEnd()
	case SelectCmdBeginning:
		moved = o.moveDocument(false)
	case SelectCmdEnd:
		moved = o.moveDocument(true)
	default:
		return SelectActionNone
	}
	if !moved {
		return SelectActionNone
	}
	o.syncSelectRange()
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
	return SelectActionChanged
}

func (o *TranscriptOverlay) startSelectMark(unit SelectUnit) {
	if !o.selectMode.active {
		return
	}
	cursor := o.selectCursor()
	if o.selectMode.marked && o.selectMode.unit == unit {
		o.selectMode.marked = false
		o.selectMode.anchor = cursor
		o.selectMode.activeEnd = cursor
		o.selectMode.preferredColumn = -1
		return
	}
	o.selectMode.marked = true
	o.selectMode.unit = unit
	o.selectMode.anchor = cursor
	o.selectMode.activeEnd = cursor
	o.selectMode.preferredColumn = -1
}

func (o *TranscriptOverlay) swapSelectEnds() {
	if !o.selectMode.active || !o.selectMode.marked {
		return
	}
	o.selectMode.anchor, o.selectMode.activeEnd = o.selectMode.activeEnd, o.selectMode.anchor
	o.selectMode.preferredColumn = -1
	o.syncSelectRange()
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
}

func (o *TranscriptOverlay) syncSelectRange() {
	if o.selectMode.marked {
		// Keep the reported anchor fixed; only the active end moved.
		return
	}
	cursor := o.selectCursor()
	o.selectMode.anchor = cursor
	o.selectMode.activeEnd = cursor
}

func (o *TranscriptOverlay) setCursor(point selectPoint) bool {
	point = o.clampPoint(point)
	if point == o.selectCursor() {
		return false
	}
	o.selectMode.activeEnd = point
	return true
}

func (o *TranscriptOverlay) clampPoint(point selectPoint) selectPoint {
	if len(o.plainLines) == 0 {
		return selectPoint{}
	}
	if point.Row < 0 {
		point.Row = 0
	}
	if point.Row >= len(o.plainLines) {
		point.Row = len(o.plainLines) - 1
	}
	point.Col = clampColumn(point.Col, o.lineWidth(point.Row))
	return point
}

func (o *TranscriptOverlay) moveCharacter(direction int) bool {
	cursor := o.selectCursor()
	if direction < 0 {
		prev, ok := prevCursorColumn(o.plainLines, cursor.Row, cursor.Col)
		if ok {
			return o.setCursor(selectPoint{Row: cursor.Row, Col: prev})
		}
		if cursor.Row == 0 {
			return false
		}
		return o.setCursor(selectPoint{Row: cursor.Row - 1, Col: o.lineWidth(cursor.Row - 1)})
	}
	next, ok := nextCursorColumn(o.plainLines, cursor.Row, cursor.Col)
	if ok {
		return o.setCursor(selectPoint{Row: cursor.Row, Col: next})
	}
	if cursor.Row >= len(o.plainLines)-1 {
		return false
	}
	return o.setCursor(selectPoint{Row: cursor.Row + 1, Col: 0})
}

func (o *TranscriptOverlay) moveRows(rows int) bool {
	if rows == 0 {
		return false
	}
	cursor := o.selectCursor()
	column := cursor.Col
	if o.selectMode.preferredColumn >= 0 {
		column = o.selectMode.preferredColumn
	}
	o.selectMode.preferredColumn = column
	target := cursor.Row + rows
	if target < 0 {
		target = 0
	}
	if target >= len(o.plainLines) {
		target = len(o.plainLines) - 1
	}
	if target == cursor.Row {
		return false
	}
	targetColumn := column
	if width := o.lineWidth(target); targetColumn > width {
		targetColumn = width
	}
	return o.setCursor(selectPoint{Row: target, Col: targetColumn})
}

func (o *TranscriptOverlay) moveWord(backward bool, toEnd bool) bool {
	cursor := o.selectCursor()
	row := cursor.Row
	for row >= 0 && row < len(o.plainLines) {
		text := o.plainLines[row]
		offset := 0
		if row == cursor.Row {
			offset = byteOffsetAtRuneIndex(text, runeIndexAtColumn(text, cursor.Col))
		} else if backward {
			offset = len(text)
		}
		var found int
		ok := false
		if backward {
			if start, exists := previousWordStart(text, offset); exists {
				found, ok = start, true
			}
		} else if toEnd {
			if end, exists := nextWordEnd(text, offset, row != cursor.Row); exists {
				found, ok = end, true
			}
		} else {
			if start, exists := nextWordStart(text, offset, row != cursor.Row); exists {
				found, ok = start, true
			}
		}
		if ok {
			return o.setCursor(selectPoint{Row: row, Col: runewidth.StringWidth(text[:found])})
		}
		if backward {
			row--
		} else {
			row++
		}
	}
	return false
}

func (o *TranscriptOverlay) moveLineStart() bool {
	cursor := o.selectCursor()
	return o.setCursor(selectPoint{Row: cursor.Row})
}

func (o *TranscriptOverlay) moveLineEnd() bool {
	cursor := o.selectCursor()
	return o.setCursor(selectPoint{Row: cursor.Row, Col: o.lineWidth(cursor.Row)})
}

func (o *TranscriptOverlay) moveDocument(end bool) bool {
	if end {
		row := len(o.plainLines) - 1
		return o.setCursor(selectPoint{Row: row, Col: o.lineWidth(row)})
	}
	return o.setCursor(selectPoint{Row: 0, Col: 0})
}

// revealSelectionCursor scrolls the viewport so the caret stays visible.
func (o *TranscriptOverlay) revealSelectionCursor() {
	if o == nil || !o.selectMode.active {
		return
	}
	cursor := o.selectCursor()
	height := o.bodyHeight()
	offset := o.viewport.YOffset
	if cursor.Row < offset {
		o.viewport.SetYOffset(cursor.Row)
		return
	}
	if cursor.Row >= offset+height {
		o.viewport.SetYOffset(cursor.Row - height + 1)
	}
}

func (o *TranscriptOverlay) beginSelectFind() {
	if o == nil {
		return
	}
	o.selectMode.searchOrigin = &selectOrigin{
		marked:          o.selectMode.marked,
		unit:            o.selectMode.unit,
		anchor:          o.selectMode.anchor,
		active:          o.selectMode.activeEnd,
		preferredColumn: o.selectMode.preferredColumn,
	}
	o.find.editing = true
	o.find.accepted = false
	o.refreshFindMatches()
}

// cancelFindAfterSelect closes a find bar that selection mode opened.
func (o *TranscriptOverlay) cancelFindAfterSelect() {
	if o == nil || !o.find.active() {
		return
	}
	o.find = transcriptFind{index: -1}
	o.refreshFindContent()
}

// acceptSelectFind moves the caret to the accepted match and extends an active
// mark (Rust move_select_cursor_to_search_match).
func (o *TranscriptOverlay) acceptSelectFind(line int) {
	if o == nil || !o.selectMode.active {
		return
	}
	point := o.selectPointForMatch(line)
	if origin := o.selectMode.searchOrigin; origin != nil {
		o.selectMode.marked = origin.marked
		o.selectMode.unit = origin.unit
		o.selectMode.anchor = origin.anchor
		o.selectMode.activeEnd = origin.active
		o.selectMode.preferredColumn = origin.preferredColumn
		o.selectMode.searchOrigin = nil
	}
	o.selectMode.preferredColumn = -1
	if o.selectMode.marked {
		o.selectMode.activeEnd = point
	} else {
		o.selectMode.anchor = point
		o.selectMode.activeEnd = point
	}
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
}

// restoreSelectFindOrigin restores the pre-search cursor after cancelling.
func (o *TranscriptOverlay) restoreSelectFindOrigin() {
	if o == nil || !o.selectMode.active {
		return
	}
	origin := o.selectMode.searchOrigin
	if origin == nil {
		return
	}
	o.selectMode.searchOrigin = nil
	o.selectMode.marked = origin.marked
	o.selectMode.unit = origin.unit
	o.selectMode.anchor = origin.anchor
	o.selectMode.activeEnd = origin.active
	o.selectMode.preferredColumn = origin.preferredColumn
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
}

// repeatSelectFind moves the caret to the next older or newer match.
func (o *TranscriptOverlay) repeatSelectFind(newer bool) bool {
	if o == nil || !o.find.accepted || len(o.find.matches) == 0 {
		return false
	}
	index := o.find.index
	if newer {
		index++
	} else {
		index--
	}
	if index < 0 || index >= len(o.find.matches) {
		return false
	}
	o.find.index = index
	point := o.selectPointForMatch(o.find.matches[index])
	o.selectMode.preferredColumn = -1
	if o.selectMode.marked {
		o.selectMode.activeEnd = point
	} else {
		o.selectMode.anchor = point
		o.selectMode.activeEnd = point
	}
	o.revealSelectionCursor()
	o.viewport.SetContent(o.viewContent())
	return true
}

// selectionSpanForRow returns the display-column span selected on one row.
func (o *TranscriptOverlay) selectionSpanForRow(row int) (int, int, bool) {
	if o == nil || !o.selectMode.active || !o.selectMode.marked {
		return 0, 0, false
	}
	start, end := o.selectionRange()
	if row < start.Row || row > end.Row {
		return 0, 0, false
	}
	width := o.lineWidth(row)
	from := 0
	to := width
	if row == start.Row {
		from = clampColumn(start.Col, width)
	}
	if row == end.Row {
		to = clampColumn(end.Col, width)
	}
	if to <= from {
		return 0, 0, false
	}
	return from, to, true
}

func clampColumn(column int, width int) int {
	if column < 0 {
		return 0
	}
	if column > width {
		return width
	}
	return column
}

// sliceByDisplayColumns returns the runes whose start column falls in
// [from, to).
func sliceByDisplayColumns(line string, from int, to int) string {
	if from >= to {
		return ""
	}
	var builder strings.Builder
	column := 0
	for _, r := range line {
		width := runewidth.RuneWidth(r)
		if width == 0 {
			width = 1
		}
		if column >= from && column < to {
			builder.WriteRune(r)
		}
		column += width
		if column >= to {
			break
		}
	}
	return builder.String()
}

// runeIndexAtColumn returns the rune index whose display span contains column.
func runeIndexAtColumn(line string, column int) int {
	offset := 0
	index := 0
	for _, r := range line {
		width := runewidth.RuneWidth(r)
		if width == 0 {
			width = 1
		}
		if column < offset+width {
			return index
		}
		offset += width
		index++
	}
	return index
}

func prevCursorColumn(lines []string, row int, column int) (int, bool) {
	if row < 0 || row >= len(lines) {
		return 0, false
	}
	if column <= 0 {
		return 0, false
	}
	offset := 0
	previous := 0
	for _, r := range lines[row] {
		width := runewidth.RuneWidth(r)
		if width == 0 {
			width = 1
		}
		if offset >= column {
			break
		}
		previous = offset
		offset += width
	}
	return previous, true
}

func nextCursorColumn(lines []string, row int, column int) (int, bool) {
	if row < 0 || row >= len(lines) {
		return 0, false
	}
	offset := 0
	for _, r := range lines[row] {
		width := runewidth.RuneWidth(r)
		if width == 0 {
			width = 1
		}
		offset += width
		if offset > column {
			return offset, true
		}
	}
	return 0, false
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordSpans returns the [start, end) rune ranges of alphanumeric words, matching
// Rust's `split_word_bound_indices().filter(|word| word.chars().any(is_alphanumeric))`.
func wordSpans(text string) [][2]int {
	runes := []rune(text)
	spans := make([][2]int, 0, len(runes)/4+1)
	index := 0
	for index < len(runes) {
		if !isWordRune(runes[index]) {
			index++
			continue
		}
		start := index
		for index < len(runes) && isWordRune(runes[index]) {
			index++
		}
		spans = append(spans, [2]int{start, index})
	}
	return spans
}

func byteOffsetAtRuneIndex(text string, runeIndex int) int {
	if runeIndex <= 0 {
		return 0
	}
	count := 0
	for offset := range text {
		if count == runeIndex {
			return offset
		}
		count++
	}
	return len(text)
}

func nextWordStart(text string, offset int, afterLineStart bool) (int, bool) {
	for _, span := range wordSpans(text) {
		byteStart := byteOffsetAtRuneIndex(text, span[0])
		if afterLineStart || byteStart > offset {
			return byteStart, true
		}
	}
	return 0, false
}

func previousWordStart(text string, offset int) (int, bool) {
	spans := wordSpans(text)
	for index := len(spans) - 1; index >= 0; index-- {
		byteStart := byteOffsetAtRuneIndex(text, spans[index][0])
		if byteStart < offset {
			return byteStart, true
		}
	}
	return 0, false
}

func nextWordEnd(text string, offset int, afterLineStart bool) (int, bool) {
	for _, span := range wordSpans(text) {
		byteEnd := byteOffsetAtRuneIndex(text, span[1]-1)
		if afterLineStart || byteEnd > offset {
			return byteEnd, true
		}
	}
	return 0, false
}
