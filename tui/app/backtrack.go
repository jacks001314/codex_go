package app

import (
	"strings"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// This file ports the pure half of Rust tui/src/app_backtrack.rs: the backtrack
// state machine and the transcript projection that turns the Nth displayed user
// message into a reopenable prompt. The overlay/key wiring lives in tui/tea and
// the fork itself in prompt_edit.go.
//
// Rust's user-position projection starts after the last SessionInfoCell. Go
// renders the session header as the first transcript message only
// (addStartupSessionHeader) and resets the transcript on a thread switch, so the
// filter degenerates to "every RoleUser message" here.

// BacktrackState aggregates the backtrack-related state owned by the model
// (Rust BacktrackState). The zero value is not primed; Reset establishes the
// canonical "no selection" value.
type BacktrackState struct {
	// Primed is true once Esc primed backtrack mode in the main view.
	Primed bool
	// BaseThreadID is the thread whose transcript is being inspected. A thread
	// change invalidates any selection made against it.
	BaseThreadID string
	// NthUserMessage indexes the filtered user-message view. It is
	// BacktrackNoSelection (and only that value) when nothing is selected.
	NthUserMessage int
	// OverlayPreviewActive is true while the transcript overlay shows a
	// backtrack preview.
	OverlayPreviewActive bool
}

// Reset clears all backtrack state (Rust reset_backtrack_state).
func (s *BacktrackState) Reset() {
	if s == nil {
		return
	}
	*s = BacktrackState{NthUserMessage: BacktrackNoSelection}
}

// Prime arms backtrack mode for the given thread and clears the selection
// (Rust prime_backtrack).
func (s *BacktrackState) Prime(threadID string) {
	if s == nil {
		return
	}
	s.Primed = true
	s.BaseThreadID = strings.TrimSpace(threadID)
	s.NthUserMessage = BacktrackNoSelection
}

// HasSelection reports whether a user message is currently selected.
func (s BacktrackState) HasSelection() bool {
	return s.NthUserMessage != BacktrackNoSelection
}

// StepBackwardBacktrack returns the ordinal of the next older user message
// (Rust step_backtrack_and_highlight): from no selection it selects the newest
// message, at the oldest it stays put, otherwise it moves one older. The result
// is BacktrackNoSelection only when the transcript has no user message.
func StepBackwardBacktrack(nthUserMessage int, count int) int {
	if count <= 0 {
		return BacktrackNoSelection
	}
	last := count - 1
	switch {
	case nthUserMessage == BacktrackNoSelection:
		return last
	case nthUserMessage <= 0:
		return 0
	default:
		next := nthUserMessage - 1
		if next > last {
			next = last
		}
		return next
	}
}

// StepForwardBacktrack returns the ordinal of the next newer user message
// (Rust step_forward_backtrack_and_highlight).
func StepForwardBacktrack(nthUserMessage int, count int) int {
	if count <= 0 {
		return BacktrackNoSelection
	}
	last := count - 1
	if nthUserMessage == BacktrackNoSelection {
		return last
	}
	next := nthUserMessage + 1
	if next > last {
		next = last
	}
	return next
}

// UserCount counts the transcript's user messages (Rust user_count).
func UserCount(messages []codextui.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == codextui.RoleUser {
			count++
		}
	}
	return count
}

// HasBacktrackTarget reports whether any user message can be reopened
// (Rust has_backtrack_target).
func HasBacktrackTarget(messages []codextui.Message) bool {
	return UserCount(messages) > 0
}

// NthUserPosition returns the transcript index of the Nth user message
// (Rust nth_user_position).
func NthUserPosition(messages []codextui.Message, nth int) (int, bool) {
	if nth < 0 {
		return 0, false
	}
	seen := 0
	for index, message := range messages {
		if message.Role != codextui.RoleUser {
			continue
		}
		if seen == nth {
			return index, true
		}
		seen++
	}
	return 0, false
}

// BacktrackSelectionForPrompt resolves the Nth user message into the composer
// state restored after the branch (Rust backtrack_selection). Go's transcript
// message carries only text, so local images and text elements are not restored
// here.
func BacktrackSelectionForPrompt(threadID string, messages []codextui.Message, nth int) (PromptEditSelection, bool) {
	index, ok := NthUserPosition(messages, nth)
	if !ok {
		return PromptEditSelection{}, false
	}
	return PromptEditSelection{
		ThreadID:    strings.TrimSpace(threadID),
		UserOrdinal: nth,
		Prompt:      chatwidget.ThreadComposerState{Text: messages[index].Text},
	}, true
}

// BacktrackSelection resolves the primed state into a selection, rejecting a
// stale base thread or an unselected state (Rust backtrack_selection).
func (s BacktrackState) BacktrackSelection(currentThreadID string, messages []codextui.Message) (PromptEditSelection, bool) {
	base := strings.TrimSpace(s.BaseThreadID)
	if base == "" || base != strings.TrimSpace(currentThreadID) || !s.HasSelection() {
		return PromptEditSelection{}, false
	}
	return BacktrackSelectionForPrompt(base, messages, s.NthUserMessage)
}
