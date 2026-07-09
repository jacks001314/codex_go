package bottompane

import "time"

// Rust parity subset: codex-rs/tui/src/bottom_pane/bottom_pane_view.rs.

type ViewCompletion string

const (
	ViewCompletionAccepted  ViewCompletion = "accepted"
	ViewCompletionCancelled ViewCompletion = "cancelled"
)

type CancellationEvent string

const (
	CancellationNotHandled CancellationEvent = "not_handled"
	CancellationHandled    CancellationEvent = "handled"
)

type ResolvedAppServerRequest struct {
	Kind       string
	ID         string
	ServerName string
	RequestID  string
}

type BottomPaneViewState struct {
	Height                       int
	Active                       bool
	Complete                     bool
	CompletionReason             *ViewCompletion
	DismissAfterChildAcceptFlag  bool
	ViewID                       string
	SelectedIndex                *int
	ActiveTab                    string
	PreferEscHandle              bool
	InterruptsTurn               bool
	TerminalTitleRequiresAction  bool
	InPasteBurst                 bool
	NextFrameDelayValue          time.Duration
	ConsumedPaste                string
	DismissedAppServerRequestKey string
}

func (s BottomPaneViewState) Visible() bool {
	return s.Active && s.Height > 0
}

func (s BottomPaneViewState) IsComplete() bool {
	return s.Complete
}

func (s BottomPaneViewState) Completion() *ViewCompletion {
	if s.CompletionReason == nil {
		return nil
	}
	reason := *s.CompletionReason
	return &reason
}

func (s *BottomPaneViewState) CompleteWith(reason ViewCompletion) {
	if s == nil {
		return
	}
	s.Complete = true
	s.CompletionReason = &reason
}

func (s *BottomPaneViewState) OnCtrlC() CancellationEvent {
	if s == nil {
		return CancellationNotHandled
	}
	s.CompleteWith(ViewCompletionCancelled)
	return CancellationHandled
}

func (s BottomPaneViewState) DismissAfterChildAccept() bool {
	return s.DismissAfterChildAcceptFlag
}

func (s *BottomPaneViewState) ClearDismissAfterChildAccept() {
	if s != nil {
		s.DismissAfterChildAcceptFlag = false
	}
}

func (s BottomPaneViewState) StableViewID() string {
	return s.ViewID
}

func (s BottomPaneViewState) SelectedIndexValue() (int, bool) {
	if s.SelectedIndex == nil {
		return 0, false
	}
	return *s.SelectedIndex, true
}

func (s BottomPaneViewState) ActiveTabID() string {
	return s.ActiveTab
}

func (s BottomPaneViewState) PreferEscToHandleKeyEvent() bool {
	return s.PreferEscHandle
}

func (s BottomPaneViewState) WillInterruptTurnOnKeyEvent() bool {
	return s.InterruptsTurn
}

func (s *BottomPaneViewState) HandlePaste(pasted string) bool {
	if s == nil || pasted == "" {
		return false
	}
	s.ConsumedPaste = pasted
	return true
}

func (s *BottomPaneViewState) FlushPasteBurstIfDue() bool {
	if s == nil || !s.InPasteBurst {
		return false
	}
	s.InPasteBurst = false
	return true
}

func (s BottomPaneViewState) IsInPasteBurst() bool {
	return s.InPasteBurst
}

func (s *BottomPaneViewState) PreDrawTick() bool {
	if s == nil {
		return false
	}
	if s.Complete && s.Active {
		s.Active = false
		return true
	}
	return false
}

func (s *BottomPaneViewState) DismissAppServerRequest(request ResolvedAppServerRequest) bool {
	if s == nil {
		return false
	}
	key := request.ServerName + "/" + request.RequestID
	if key == "/" {
		key = request.Kind + "/" + request.ID
	}
	if s.DismissedAppServerRequestKey != "" && s.DismissedAppServerRequestKey != key {
		return false
	}
	s.DismissedAppServerRequestKey = key
	s.CompleteWith(ViewCompletionCancelled)
	return true
}

func (s BottomPaneViewState) TerminalTitleRequiresActionNow() bool {
	return s.TerminalTitleRequiresAction && s.Active && !s.Complete
}

func (s BottomPaneViewState) NextFrameDelay() (time.Duration, bool) {
	if s.NextFrameDelayValue <= 0 {
		return 0, false
	}
	return s.NextFrameDelayValue, true
}

func BottomPaneViewStateWithSelection(height int, selected int) BottomPaneViewState {
	return BottomPaneViewState{
		Height:        height,
		Active:        true,
		SelectedIndex: &selected,
	}
}
