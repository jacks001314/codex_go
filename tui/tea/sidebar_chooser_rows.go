package tea

import (
	"strings"

	codextui "codex_go/tui"
	"github.com/rivo/uniseg"
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
// chooser index (new chat, chats, retry row, files), plus the header rows.
type sidebarChoiceRows struct {
	wide        bool
	height      int
	chatsHeader int
	filesHeader int
	choices     []sidebarChooserSpan
}

// sidebarChooserBodyStart is Rust CHOOSER_BODY_START: the first body row below
// the tab strip stays blank.
const sidebarChooserBodyStart = 1

func (m *Model) sidebarChoiceRowsFor(width int, height int) sidebarChoiceRows {
	rows := sidebarChoiceRows{chatsHeader: -1, filesHeader: -1}
	if m == nil {
		return rows
	}
	choices := m.sidebarChooserChoices()
	files := m.sidebarFiles
	rows.wide = sidebarChooserBodyWidth(width) >= sidebarChooserWideWidth
	rows.height = max(height-3, 0)
	fileStart := m.sidebarChooserFileStartFor(choices)
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
		if index == fileStart {
			rows.filesHeader = row + 1
			row += 3
		}
		start := row
		subtitle := false
		if index == 0 || file {
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

// sidebarChooserFilePathLayout is Rust file_path_layout: the wrapped path rows
// and how many of them fit between the "File path" caption and the footer.
func sidebarChooserFilePathLayout(width int, height int, path string) ([]string, int) {
	body := height
	if height > 2 {
		body -= 3
	}
	visible := body
	if body > 2 {
		visible -= 2
	}
	return wrapSidebarFilePath(path, max(sidebarChooserBodyWidth(width), 1)), max(visible, 0)
}

// scrollSidebarFilePath is Rust scroll_file_path.
func scrollSidebarFilePath(width int, height int, path string, top int, action string) int {
	rows, visible := sidebarChooserFilePathLayout(width, height, path)
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

// wrapSidebarFilePath hard-wraps a path the way textwrap::wrap breaks a long
// unbroken word, so the file-path view shows every character.
func wrapSidebarFilePath(path string, width int) []string {
	if path == "" {
		return nil
	}
	if width <= 0 {
		return []string{path}
	}
	lines := make([]string, 0, 4)
	rest := path
	for rest != "" {
		prefix, suffix, taken := codextui.TakePrefixByWidth(rest, width)
		if taken == 0 {
			graphemes := uniseg.NewGraphemes(rest)
			if !graphemes.Next() {
				break
			}
			_, end := graphemes.Positions()
			prefix, suffix = rest[:end], rest[end:]
		}
		lines = append(lines, strings.TrimRight(prefix, " "))
		rest = strings.TrimLeft(suffix, " ")
	}
	return lines
}
