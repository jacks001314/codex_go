package tui

import "time"

// Rust parity: codex-rs/tui/src/transcript_reflow.rs.

const TranscriptReflowDebounce = 75 * time.Millisecond

type TranscriptReflowState struct {
	lastObservedWidth           *int
	lastReflowWidth             *int
	pendingReflowWidth          *int
	pendingUntil                *time.Time
	ranDuringStream             bool
	resizeRequestedDuringStream bool
}

type TranscriptWidthChange struct {
	Changed     bool
	Initialized bool
}

func ReflowTranscriptLines(lines []string, width int) []string {
	return WrapLines(lines, WrapOptions{Width: width, BreakWords: true})
}

func (s *TranscriptReflowState) Clear() {
	if s == nil {
		return
	}
	*s = TranscriptReflowState{}
}

func (s *TranscriptReflowState) NoteWidth(width int) TranscriptWidthChange {
	if s == nil {
		return TranscriptWidthChange{Initialized: true}
	}
	previous := s.lastObservedWidth
	s.lastObservedWidth = intPtr(width)
	if previous == nil {
		s.lastReflowWidth = intPtr(width)
	}
	return TranscriptWidthChange{
		Changed:     previous != nil && *previous != width,
		Initialized: previous == nil,
	}
}

func (s *TranscriptReflowState) ReflowNeededForWidth(width int) bool {
	if s == nil {
		return false
	}
	return !intPtrEqual(s.lastReflowWidth, width) && !intPtrEqual(s.pendingReflowWidth, width)
}

func (s *TranscriptReflowState) ScheduleDebounced(targetWidth *int) bool {
	if s == nil {
		return false
	}
	if targetWidth != nil {
		s.pendingReflowWidth = intPtr(*targetWidth)
	}
	deadline := time.Now().Add(TranscriptReflowDebounce)
	s.pendingUntil = &deadline
	return false
}

func (s *TranscriptReflowState) ScheduleImmediate() {
	if s == nil {
		return
	}
	s.pendingReflowWidth = nil
	now := time.Now()
	s.pendingUntil = &now
}

func (s *TranscriptReflowState) SetDueForTest() {
	if s == nil {
		return
	}
	due := time.Now().Add(-time.Millisecond)
	s.pendingUntil = &due
}

func (s *TranscriptReflowState) PendingIsDue(now time.Time) bool {
	return s != nil && s.pendingUntil != nil && !now.Before(*s.pendingUntil)
}

func (s *TranscriptReflowState) PendingUntil() *time.Time {
	if s == nil || s.pendingUntil == nil {
		return nil
	}
	value := *s.pendingUntil
	return &value
}

func (s *TranscriptReflowState) HasPendingReflow() bool {
	return s != nil && s.pendingUntil != nil
}

func (s *TranscriptReflowState) ClearPendingReflow() {
	if s == nil {
		return
	}
	s.pendingUntil = nil
	s.pendingReflowWidth = nil
}

func (s *TranscriptReflowState) MarkReflowedWidth(width int) bool {
	if s == nil {
		return false
	}
	changed := !intPtrEqual(s.lastReflowWidth, width)
	s.lastReflowWidth = intPtr(width)
	return changed
}

func (s *TranscriptReflowState) MarkRanDuringStream() {
	if s != nil {
		s.ranDuringStream = true
	}
}

func (s *TranscriptReflowState) MarkResizeRequestedDuringStream() {
	if s != nil {
		s.resizeRequestedDuringStream = true
	}
}

func (s *TranscriptReflowState) TakeStreamFinishReflowNeeded() bool {
	if s == nil {
		return false
	}
	needed := s.ranDuringStream || s.resizeRequestedDuringStream
	s.ranDuringStream = false
	s.resizeRequestedDuringStream = false
	return needed
}

func (s *TranscriptReflowState) ClearStreamFlags() {
	if s == nil {
		return
	}
	s.ranDuringStream = false
	s.resizeRequestedDuringStream = false
}

func intPtr(value int) *int {
	return &value
}

func intPtrEqual(value *int, target int) bool {
	return value != nil && *value == target
}
