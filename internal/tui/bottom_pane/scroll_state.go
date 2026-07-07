package bottompane

// Rust parity: codex-rs/tui/src/bottom_pane/scroll_state.rs.

type ScrollState struct {
	SelectedIdx  int
	HasSelection bool
	ScrollTop    int
}

func NewScrollState() ScrollState {
	return ScrollState{}
}

func (s *ScrollState) Reset() {
	s.SelectedIdx = 0
	s.HasSelection = false
	s.ScrollTop = 0
}

func (s *ScrollState) ClampSelection(length int) {
	if s.clearIfEmpty(length) {
		return
	}
	if !s.HasSelection {
		s.SelectedIdx = 0
		s.HasSelection = true
		return
	}
	if s.SelectedIdx >= length {
		s.SelectedIdx = length - 1
	}
	if s.SelectedIdx < 0 {
		s.SelectedIdx = 0
	}
}

func (s *ScrollState) MoveUpWrap(length int) {
	if s.clearIfEmpty(length) {
		return
	}
	if !s.HasSelection || s.SelectedIdx <= 0 {
		s.SelectedIdx = length - 1
	} else {
		s.SelectedIdx--
	}
	s.HasSelection = true
}

func (s *ScrollState) MoveDownWrap(length int) {
	if s.clearIfEmpty(length) {
		return
	}
	if !s.HasSelection || s.SelectedIdx+1 >= length {
		s.SelectedIdx = 0
	} else {
		s.SelectedIdx++
	}
	s.HasSelection = true
}

func (s *ScrollState) PageUpClamped(length int, visibleRows int) {
	if s.clearIfEmpty(length) {
		return
	}
	step := max(visibleRows, 1)
	s.ClampSelection(length)
	s.SelectedIdx = max(s.SelectedIdx-step, 0)
	s.EnsureVisible(length, visibleRows)
}

func (s *ScrollState) PageDownClamped(length int, visibleRows int) {
	if s.clearIfEmpty(length) {
		return
	}
	step := max(visibleRows, 1)
	s.ClampSelection(length)
	s.SelectedIdx = min(s.SelectedIdx+step, length-1)
	s.EnsureVisible(length, visibleRows)
}

func (s *ScrollState) JumpTop(length int, visibleRows int) {
	if s.clearIfEmpty(length) {
		return
	}
	s.SelectedIdx = 0
	s.HasSelection = true
	s.EnsureVisible(length, visibleRows)
}

func (s *ScrollState) JumpBottom(length int, visibleRows int) {
	if s.clearIfEmpty(length) {
		return
	}
	s.SelectedIdx = length - 1
	s.HasSelection = true
	s.EnsureVisible(length, visibleRows)
}

func (s *ScrollState) EnsureVisible(length int, visibleRows int) {
	if length == 0 || visibleRows == 0 {
		s.ScrollTop = 0
		return
	}
	if !s.HasSelection {
		s.ScrollTop = 0
		return
	}
	if s.SelectedIdx < s.ScrollTop {
		s.ScrollTop = s.SelectedIdx
		return
	}
	bottom := s.ScrollTop + visibleRows - 1
	if s.SelectedIdx > bottom {
		s.ScrollTop = s.SelectedIdx + 1 - visibleRows
	}
}

func (s *ScrollState) clearIfEmpty(length int) bool {
	if length > 0 {
		return false
	}
	s.SelectedIdx = 0
	s.HasSelection = false
	s.ScrollTop = 0
	return true
}
