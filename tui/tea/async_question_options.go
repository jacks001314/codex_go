package tea

import (
	codextui "codex_go/tui"
	"codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/bottom_pane/async_questions/options.rs
// (#62209). A choice taller than the answer area is paged by visual line while
// it stays selected, and only content that has actually been painted may
// become user authorization.

// asyncQuestionOptionsViewport tracks the painted window over the expanded
// question's choice lines. Offsets and counters are in visual-line space across
// every choice, so a page can straddle choices exactly like Rust's viewport.
type asyncQuestionOptionsViewport struct {
	valid       bool
	questionID  string
	width       int
	height      int
	offset      int
	total       int
	visibleFrom int
	visibleTo   int
	selected    int
	// selectedStart and selectedHeight locate the focused choice; seenEnd
	// counts its leading lines painted in order at full width.
	selectedStart       int
	selectedHeight      int
	seenEnd             int
	horizontallyClipped map[int]bool
}

// boolRow maps a rendered row's presence to one line.
func boolRow(present bool) int {
	if present {
		return 1
	}
	return 0
}

// update records the geometry of the next window. paint is true from the
// renderer: only then may the selected choice's lines count as seen.
func (v *asyncQuestionOptionsViewport) update(questionID string, groups [][]string, width int, height int, selected int, paint bool) {
	if width < 1 {
		width = 1
	}
	if height < 0 {
		height = 0
	}
	starts := make([]int, len(groups))
	total := 0
	for index, lines := range groups {
		starts[index] = total
		total += len(lines)
	}
	selectedStart, selectedHeight := 0, 0
	if selected >= 0 && selected < len(groups) {
		selectedStart = starts[selected]
		selectedHeight = len(groups[selected])
	}
	if !v.valid || v.questionID != questionID || v.selected != selected || v.width != width {
		v.seenEnd = 0
		if !v.valid {
			v.offset = 0
		}
		if selectedStart < v.offset || selectedHeight > height {
			v.offset = selectedStart
		} else if selectedStart+selectedHeight > v.offset+height {
			v.offset = selectedStart + selectedHeight - height
		}
	} else if v.height != height && selectedHeight <= height {
		minOffset := selectedStart + selectedHeight - height
		if minOffset < 0 {
			minOffset = 0
		}
		if v.offset > selectedStart {
			v.offset = selectedStart
		}
		if v.offset < minOffset {
			v.offset = minOffset
		}
	}
	v.valid = true
	v.questionID = questionID
	v.width = width
	v.height = height
	v.total = total
	v.selected = selected
	v.selectedStart = selectedStart
	v.selectedHeight = selectedHeight
	maxOffset := total - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if v.offset > maxOffset {
		v.offset = maxOffset
	}
	if v.offset < 0 {
		v.offset = 0
	}
	firstFull := -1
	for index := range groups {
		if starts[index] >= v.offset && starts[index]+len(groups[index]) <= v.offset+height {
			firstFull = index
			break
		}
	}
	fullCount := 0
	if firstFull >= 0 {
		for index := firstFull; index < len(groups); index++ {
			if starts[index] < v.offset || starts[index]+len(groups[index]) > v.offset+height {
				break
			}
			fullCount++
		}
	}
	if firstFull < 0 {
		firstFull = 0
	}
	v.visibleFrom = firstFull
	v.visibleTo = firstFull + fullCount
	v.horizontallyClipped = nil
	for index := v.visibleFrom; index < v.visibleTo; index++ {
		for _, line := range groups[index] {
			if codextui.DisplayWidth(line) > width {
				if v.horizontallyClipped == nil {
					v.horizontallyClipped = map[int]bool{}
				}
				v.horizontallyClipped[index] = true
				break
			}
		}
	}
	if paint && selected >= 0 && selected < len(groups) {
		// Only lines painted in order advance the seen counter: the first
		// painted row must continue exactly where the previous paint stopped,
		// so scrolling past unread middle lines cannot authorize the choice.
		from := 0
		if v.offset > selectedStart {
			from = v.offset - selectedStart
		}
		to := selectedHeight
		if v.offset+height < selectedStart+selectedHeight {
			to = v.offset + height - selectedStart
		}
		for relative := from; relative < to; relative++ {
			if relative != v.seenEnd {
				break
			}
			if codextui.DisplayWidth(groups[selected][relative]) > width {
				break
			}
			v.seenEnd++
		}
	}
}

// matches reports whether the viewport describes the current choice.
func (v *asyncQuestionOptionsViewport) matches(questionID string, selected int) bool {
	return v.valid && v.questionID == questionID && v.selected == selected
}

// selectedPage scrolls one page when the focused choice is taller than the
// window, mirroring Rust's OptionViewport::selected_page.
func (v *asyncQuestionOptionsViewport) selectedPage(down bool) bool {
	step := v.height
	if step == 0 || v.selectedHeight <= step {
		return false
	}
	if down && v.offset+step < v.selectedStart+v.selectedHeight {
		v.scroll(step)
		return true
	}
	if !down && v.offset > v.selectedStart {
		delta := v.offset - v.selectedStart
		if delta > step {
			delta = step
		}
		v.scroll(-delta)
		return true
	}
	return false
}

func (v *asyncQuestionOptionsViewport) scroll(delta int) {
	if delta > v.height {
		delta = v.height
	}
	if delta < -v.height {
		delta = -v.height
	}
	offset := v.offset + delta
	maxOffset := v.total - v.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	v.offset = offset
}

// fullySeen reports whether the choice may become user authorization: either
// every line is inside the window at full width, or the choice is taller than
// the window and every line was painted in order while paging.
func (v *asyncQuestionOptionsViewport) fullySeen(index int) bool {
	if !v.valid || index < 0 {
		return false
	}
	if index >= v.visibleFrom && index < v.visibleTo && !v.horizontallyClipped[index] {
		return true
	}
	if v.selected != index {
		return false
	}
	if v.height == 0 || v.selectedHeight <= v.height {
		return false
	}
	if v.offset < v.selectedStart || v.offset >= v.selectedStart+v.selectedHeight {
		return false
	}
	return v.seenEnd == v.selectedHeight
}

// asyncQuestionOptionsAreaHeight bounds the choice rows painted below the
// question title. The existing block budget keeps the transcript readable; the
// title, progress label, hint line, and composer row are reserved around it.
func (m *Model) asyncQuestionOptionsAreaHeight(titleRows int, hintRows int, progressRows int) int {
	budget := m.height - 3 - minTranscriptHeight
	if m.regionChromeEnabled() {
		budget -= 3
	}
	available := budget - titleRows - hintRows - progressRows - 1
	if available < 1 {
		return 1
	}
	return available
}

// ensureAsyncQuestionOptionsViewport measures the window for the current
// question. paint only advances the seen counter; key handling and submission
// checks measure without authorizing lines that have not been displayed.
func (m *Model) ensureAsyncQuestionOptionsViewport(question bottompane.AsyncUserInputQuestion, paint bool) {
	width := max(m.width-2, 8)
	titleRows := len(wrapAsyncQuestionTitleLines(question.Title, width))
	hintRows := 0
	if hint := m.asyncQuestionHintLine(); hint != "" {
		hintRows = 1
	}
	progressRows := boolRow(m.asyncQuestions.UnansweredCount() > 1)
	height := m.asyncQuestionOptionsAreaHeight(titleRows, hintRows, progressRows)
	groups := m.asyncQuestionOptionLineGroups(question, width)
	m.asyncQuestionOptions.update(m.asyncQuestions.CurrentQuestionID(), groups, width, height, m.asyncQuestions.SelectedOptionIndex(), paint)
}

// pageAsyncQuestionOption pages the focused choice's lines instead of moving
// the selection. It reports true when the key was consumed. Like Rust, a key
// that arrives before the current choice has been painted is consumed while
// the next frame supplies its measurements.
func (m *Model) pageAsyncQuestionOption(down bool) bool {
	question, ok := m.asyncQuestions.CurrentQuestion()
	if !ok || len(question.Options) == 0 || m.asyncQuestions.FocusIsNotes() {
		return false
	}
	if !m.asyncQuestionOptions.matches(m.asyncQuestions.CurrentQuestionID(), m.asyncQuestions.SelectedOptionIndex()) {
		return true
	}
	return m.asyncQuestionOptions.selectedPage(down)
}
