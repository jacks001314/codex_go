package tea

import (
	"strings"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/sidebar_chooser.rs ChoiceRows, ChooserScroll,
// navigate, wheel_scroll and the file-path view (#61920/#61922).

// sidebarChooserScroll is Rust ChooserScroll: the painted top row plus a flag
// that the wheel moved the viewport away from the keyboard selection.
type sidebarChooserScroll struct {
	top           int
	wheelScrolled bool
}

// sidebarChoiceRows mirrors Rust ChoiceRows: the painted row range of every
// chooser index (new chat, chats, retry row, subagents, files), plus the
// header rows.
type sidebarChoiceRows struct {
	wide         bool
	height       int
	chatsHeader  int
	agentsHeader int
	filesHeader  int
	choices      []sidebarChooserSpan
}

// sidebarChooserBodyStart is Rust CHOOSER_BODY_START: the first body row below
// the tab strip stays blank.
const sidebarChooserBodyStart = 1

func (m *Model) sidebarChoiceRowsFor(width int, height int) sidebarChoiceRows {
	rows := sidebarChoiceRows{chatsHeader: -1, agentsHeader: -1, filesHeader: -1}
	if m == nil {
		return rows
	}
	choices := m.sidebarChooserChoices()
	files := m.sidebarFiles
	rows.wide = sidebarChooserBodyWidth(width) >= sidebarChooserWideWidth
	rows.height = max(height-3, 0)
	fileStart := m.sidebarChooserFileStartFor(choices)
	agentStart := m.sidebarChooserAgentStartFor(choices)
	retryVisible := m.sidebarChooserRetryVisible(choices)
	count := fileStart + len(files)
	row := sidebarChooserFirstChoiceRow
	rows.choices = make([]sidebarChooserSpan, 0, count)
	for index := 0; index < count; index++ {
		if index == 1 && len(choices) > 0 {
			rows.chatsHeader = row + 1
			row += 3
		} else if index == 1 && retryVisible {
			row += 2
		}
		file := index >= fileStart
		agent := index >= agentStart && !file
		if agent && index == agentStart {
			rows.agentsHeader = row + 1
			row += 3
		}
		if index == fileStart {
			rows.filesHeader = row + 1
			row += 3
		}
		start := row
		subtitle := false
		if index == 0 || file || agent {
			subtitle = !rows.wide
		} else if index-1 < len(choices) {
			subtitle = m.sidebarChooserPrompt(choices[index-1]) != ""
		}
		if subtitle {
			row++
		}
		row++
		rows.choices = append(rows.choices, sidebarChooserSpan{start: start, end: row})
	}
	return rows
}

func (r sidebarChoiceRows) maxTop() int {
	if len(r.choices) == 0 {
		return 0
	}
	return max(r.choices[len(r.choices)-1].end-(sidebarChooserBodyStart+r.height), 0)
}

// top is Rust ChoiceRows::top: the wheel scroll is clamped, while keyboard
// movement scrolls just far enough to show the selection and its subtitle.
func (r sidebarChoiceRows) top(scroll sidebarChooserScroll, choice int) int {
	top := clampViewportOffset(scroll.top, 0, r.maxTop())
	if scroll.wheelScrolled || r.height == 0 {
		return top
	}
	if choice == 0 && top == 0 {
		return 0
	}
	if len(r.choices) == 0 {
		return top
	}
	choice = clampViewportOffset(choice, 0, len(r.choices)-1)
	lines := r.choices[choice]
	latest := max(lines.start-sidebarChooserBodyStart, 0)
	earliest := max(lines.end-(sidebarChooserBodyStart+r.height), 0)
	top = clampViewportOffset(top, min(earliest, latest), latest)
	return min(top, r.maxTop())
}

// visible is Rust ChoiceRows::visible_choices: the choices intersecting the
// body window starting at CHOOSER_BODY_START + top.
func (r sidebarChoiceRows) visible(scroll sidebarChooserScroll, choice int) []sidebarChooserVisibleChat {
	start := sidebarChooserBodyStart + r.top(scroll, choice)
	end := start + r.height
	if start >= end {
		return nil
	}
	visible := make([]sidebarChooserVisibleChat, 0, len(r.choices))
	for index, lines := range r.choices {
		if lines.end <= start {
			continue
		}
		if lines.start >= end {
			break
		}
		visible = append(visible, sidebarChooserVisibleChat{index: index, rows: lines})
	}
	return visible
}

// wheelScroll is Rust wheel_scroll: the wheel moves the viewport itself and
// leaves the keyboard selection where it was.
func (r sidebarChoiceRows) wheelScroll(scroll sidebarChooserScroll, choice int, delta int) sidebarChooserScroll {
	return sidebarChooserScroll{
		top:           clampViewportOffset(r.top(scroll, choice)+delta, 0, r.maxTop()),
		wheelScrolled: true,
	}
}

// navigate is Rust sidebar_chooser::navigate. It returns the new selection and
// scroll state; the callers keep painting the shared layout.
func (r sidebarChoiceRows) navigate(scroll sidebarChooserScroll, choice int, action string) (int, sidebarChooserScroll) {
	if len(r.choices) == 0 {
		return 0, scroll
	}
	top := r.top(scroll, choice)
	last := len(r.choices) - 1
	choice = clampViewportOffset(choice, 0, last)
	selected := r.choices[choice]
	height := max(r.height, 1)
	visible := r.visible(scroll, choice)
	switch action {
	case "jump_top":
		return 0, sidebarChooserScroll{}
	case "jump_bottom":
		choice = last
	case "move_up", "page_up":
		if selected.start >= sidebarChooserBodyStart+top+r.height {
			if len(visible) > 0 {
				choice = visible[len(visible)-1].index
			}
			break
		}
		if action == "move_up" {
			choice = max(choice-1, 0)
			break
		}
		choice = min(partitionPointChoiceRows(r.choices, selected.start-height), max(choice-1, 0))
	case "move_down", "page_down":
		if selected.end <= sidebarChooserBodyStart+top {
			if len(visible) > 0 {
				choice = visible[0].index
			}
			break
		}
		if action == "move_down" {
			choice = min(choice+1, last)
			break
		}
		choice = max(partitionPointChoiceStarts(r.choices, selected.start+height)-1, min(choice+1, last))
	default:
		return choice, scroll
	}
	if choice == 0 {
		return choice, sidebarChooserScroll{}
	}
	next := sidebarChooserScroll{top: top}
	next.top = r.top(next, choice)
	return choice, next
}

// partitionPointChoiceRows is Rust `choices.partition_point(|lines| lines.end
// <= boundary)`: the first index whose rows end at or after the boundary.
func partitionPointChoiceRows(choices []sidebarChooserSpan, boundary int) int {
	low, high := 0, len(choices)
	for low < high {
		mid := int(uint(low+high) >> 1)
		if choices[mid].end <= boundary {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low
}

// partitionPointChoiceStarts is Rust `partition_point(|lines| lines.start <=
// boundary)`: the first index whose rows start after the boundary.
func partitionPointChoiceStarts(choices []sidebarChooserSpan, boundary int) int {
	low, high := 0, len(choices)
	for low < high {
		mid := int(uint(low+high) >> 1)
		if choices[mid].start <= boundary {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low
}

// navigateSidebarChooser applies one list action with Rust navigate().
func (m *Model) navigateSidebarChooser(action string) {
	if m == nil {
		return
	}
	rows := m.sidebarChoiceRowsFor(m.sidebarTabStripWidth(), m.sidebarChooserPanelHeight())
	choice, scroll := rows.navigate(m.sidebarChooserScroll, m.sidebarChooserClampedChoice(), action)
	m.sidebarChoice = choice
	m.sidebarChooserScroll = scroll
}

// sidebarChooserSelectionVisible reports whether the selection is inside the
// painted body window (Rust choice_is_visible).
func (m *Model) sidebarChooserSelectionVisible() bool {
	if m == nil {
		return false
	}
	rows := m.sidebarChoiceRowsFor(m.sidebarTabStripWidth(), m.sidebarChooserPanelHeight())
	choice := m.sidebarChooserClampedChoice()
	for _, visible := range rows.visible(m.sidebarChooserScroll, choice) {
		if visible.index == choice {
			return true
		}
	}
	return false
}

// sidebarChooserSelectedFile reports whether the selection is a Files row.
func (m *Model) sidebarChooserSelectedFile() bool {
	if m == nil {
		return false
	}
	_, ok := m.sidebarChooserFile(m.sidebarChooserClampedChoice())
	return ok
}

// wheelScrollSidebarChooser is Rust wheel_scroll: it moves the viewport and
// leaves the keyboard selection alone.
func (m *Model) wheelScrollSidebarChooser(delta int) {
	if m == nil {
		return
	}
	rows := m.sidebarChoiceRowsFor(m.sidebarTabStripWidth(), m.sidebarChooserPanelHeight())
	m.sidebarChooserScroll = rows.wheelScroll(m.sidebarChooserScroll, m.sidebarChooserClampedChoice(), delta)
}

// sidebarFilePathBody is Rust sidebar_chooser::file_path_areas' body rect: it
// skips the strip rows above and the one-row footer below, and narrows to
// body_row's columns.
func sidebarFilePathBody(width int, height int) (bodyWidth int, bodyHeight int, top int) {
	footer := min(height, 1)
	top = footer
	if height > 3 {
		top++
	}
	return max(width-3, 1), max(height-(top+footer), 0), top
}

// sidebarFilePathVisible is the wrapped-path window inside the body: the
// caption and its blank row only exist when the body has room for them.
func sidebarFilePathVisible(bodyHeight int) int {
	if bodyHeight > 2 {
		return bodyHeight - 2
	}
	return max(bodyHeight, 0)
}

// sidebarChooserFilePathLayout is Rust file_path_layout: the wrapped path rows
// and how many of them fit between the "File path" caption and the footer.
func sidebarChooserFilePathLayout(width int, height int, path string) ([]string, int) {
	bodyWidth, bodyHeight, _ := sidebarFilePathBody(width, height)
	return wrapSidebarFilePath(path, bodyWidth), sidebarFilePathVisible(bodyHeight)
}

// scrollSidebarFilePath is Rust scroll_file_path.
func scrollSidebarFilePath(width int, height int, path string, top int, action string) int {
	bodyWidth, bodyHeight, _ := sidebarFilePathBody(width, height)
	return scrollSidebarFilePathBody(bodyWidth, bodyHeight, path, top, action)
}

// scrollSidebarFilePathBody scrolls the path view inside an already split body
// (Rust scroll_file_path takes that body rect directly).
func scrollSidebarFilePathBody(bodyWidth int, bodyHeight int, path string, top int, action string) int {
	rows := wrapSidebarFilePath(path, bodyWidth)
	visible := sidebarFilePathVisible(bodyHeight)
	limit := max(len(rows)-visible, 0)
	top = clampViewportOffset(top, 0, limit)
	switch action {
	case "move_up":
		return max(top-1, 0)
	case "move_down":
		return min(top+1, limit)
	case "page_up":
		return max(top-visible, 0)
	case "page_down":
		return min(top+visible, limit)
	case "jump_top":
		return 0
	case "jump_bottom":
		return limit
	}
	return top
}

// wrapSidebarFilePath is Rust file_path_layout's wrap: the path splits after
// each directory separator, segments fill a row while they fit, and a segment
// wider than the pane is broken so every character stays visible.
func wrapSidebarFilePath(path string, width int) []string {
	if path == "" {
		return nil
	}
	if width <= 0 {
		return []string{path}
	}
	lines := make([]string, 0, 4)
	current := ""
	for _, segment := range sidebarFilePathSegments(path) {
		segmentWidth := codextui.DisplayWidth(segment)
		if current != "" && codextui.DisplayWidth(current)+segmentWidth > width {
			lines = append(lines, strings.TrimRight(current, " "))
			current = ""
		}
		if current == "" && segmentWidth > width {
			parts := sidebarDocumentErrorHardWrap(segment, width)
			lines = append(lines, parts[:len(parts)-1]...)
			current = parts[len(parts)-1]
			continue
		}
		current += segment
	}
	if current != "" {
		lines = append(lines, strings.TrimRight(current, " "))
	}
	if len(lines) == 0 {
		return []string{path}
	}
	return lines
}

// sidebarFilePathSegments splits a path after each separator, keeping the
// separator with the preceding name (Rust split_inclusive(['/', '\\'])).
func sidebarFilePathSegments(path string) []string {
	segments := make([]string, 0, 4)
	start := 0
	for index, r := range path {
		if r == '/' || r == '\\' {
			segments = append(segments, path[start:index+1])
			start = index + 1
		}
	}
	if start < len(path) {
		segments = append(segments, path[start:])
	}
	return segments
}
