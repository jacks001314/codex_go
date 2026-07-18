package chatwidget

const (
	SafetyBufferingPromptViewID     = "safety-buffering-prompt"
	SafetyBufferingMessageWithRetry = "This request requires additional safety checks, which can take extra time. Hang tight or retry with a faster model for a quicker response, though it may be less capable of handling complex requests."
	SafetyBufferingMessageNoRetry   = "This request requires additional safety checks, which can take extra time."
)

const (
	SafetyBufferingActionRetry UsageMenuAction = "safety_buffering_retry"
	SafetyBufferingActionWait  UsageMenuAction = "safety_buffering_wait"
)

type SafetyBufferingState struct {
	SubmittedTurnID     string
	ActiveTurnID        string
	RetryPromptShown    bool
	AgentMessageStarted bool
}

type SafetyBufferingContext struct {
	Replay           ReplayKind
	AgentTurnRunning bool
	LastTurnID       string
	ThreadID         string
	EnforceTurn      bool
}

type SafetyBufferingUpdate struct {
	TurnID          string
	ShowBufferingUI bool
	FasterModel     string
	CanRetry        bool
}

type SafetyBufferingResult struct {
	Status         StatusIndicatorState
	Prompt         *SelectionView
	Cleared        bool
	Waiting        bool
	RetryAvailable bool
	DismissPrompt  bool
	Ignored        bool
	Redraw         bool
}

type SafetyBufferingRetryResult struct {
	Prepared                  bool
	Failed                    bool
	UserTurnPendingStart      bool
	FinalizeTurn              bool
	ClearLastRenderedUserText bool
	RestoredPrompt            *UserMessage
}

func (s *SafetyBufferingState) RecordTurn(turnID string) {
	if s == nil {
		return
	}
	s.SubmittedTurnID = turnID
}

func (s *SafetyBufferingState) Clear() {
	if s == nil {
		return
	}
	*s = SafetyBufferingState{}
}

func (s *SafetyBufferingState) ResetForTurnStart() SafetyBufferingResult {
	if s == nil {
		return SafetyBufferingResult{}
	}
	s.ActiveTurnID = ""
	s.RetryPromptShown = false
	s.AgentMessageStarted = false
	return SafetyBufferingResult{DismissPrompt: true, Cleared: true}
}

func (s *SafetyBufferingState) MarkAgentMessageStarted() {
	if s == nil {
		return
	}
	if s.ActiveTurnID != "" {
		s.AgentMessageStarted = true
	}
}

func (s *SafetyBufferingState) IsWaiting() bool {
	return s != nil && s.ActiveTurnID != "" && !s.AgentMessageStarted
}

func (s *SafetyBufferingState) CanRetry(turnID string) bool {
	return s != nil && s.ActiveTurnID == turnID && !s.AgentMessageStarted
}

func (s *SafetyBufferingState) Apply(update SafetyBufferingUpdate) SafetyBufferingResult {
	return s.ApplyWithContext(update, SafetyBufferingContext{})
}

func (s *SafetyBufferingState) ApplyWithContext(update SafetyBufferingUpdate, context SafetyBufferingContext) SafetyBufferingResult {
	if s == nil || update.TurnID == "" {
		return SafetyBufferingResult{}
	}
	if context.Replay == ReplayResumeInitialMessages ||
		(context.EnforceTurn && (!context.AgentTurnRunning || context.LastTurnID != update.TurnID)) {
		return SafetyBufferingResult{Ignored: true}
	}
	if !update.ShowBufferingUI {
		if s.ActiveTurnID == update.TurnID {
			s.ActiveTurnID = ""
			s.RetryPromptShown = false
			s.AgentMessageStarted = false
			return SafetyBufferingResult{Cleared: true, DismissPrompt: true, Redraw: true}
		}
		return SafetyBufferingResult{}
	}
	canRetry := update.CanRetry &&
		update.FasterModel != "" &&
		s.SubmittedTurnID == update.TurnID &&
		(context.Replay == ReplayNone) &&
		(!context.EnforceTurn || context.ThreadID != "")
	shouldShowPrompt := canRetry && !s.RetryPromptShown
	if shouldShowPrompt {
		s.RetryPromptShown = true
	}
	s.ActiveTurnID = update.TurnID
	message := SafetyBufferingMessageNoRetry
	if canRetry {
		message = SafetyBufferingMessageWithRetry
	}
	result := SafetyBufferingResult{
		Status: StatusIndicatorState{
			Header:          "Working",
			Details:         message,
			DetailsMaxLines: 6,
		},
		Waiting:        !s.AgentMessageStarted,
		RetryAvailable: canRetry,
		DismissPrompt:  !canRetry,
		Redraw:         true,
	}
	if shouldShowPrompt {
		view := NewSafetyBufferingPromptView(update.FasterModel)
		result.Prompt = &view
	}
	return result
}

func (s *SafetyBufferingState) PrepareRetry(queue *InputQueueState) SafetyBufferingRetryResult {
	if queue != nil {
		queue.UserTurnPendingStart = true
	}
	return SafetyBufferingRetryResult{
		Prepared:                  true,
		UserTurnPendingStart:      queue != nil && queue.UserTurnPendingStart,
		FinalizeTurn:              true,
		ClearLastRenderedUserText: true,
	}
}

func (s *SafetyBufferingState) FailRetry(queue *InputQueueState, cancelEdit *CancelEditState) SafetyBufferingRetryResult {
	if queue != nil {
		queue.UserTurnPendingStart = false
	}
	if s != nil {
		s.Clear()
	}
	var restored *UserMessage
	if cancelEdit != nil && cancelEdit.Prompt != nil {
		prompt := *cancelEdit.Prompt
		restored = &prompt
		cancelEdit.Clear()
	}
	return SafetyBufferingRetryResult{
		Failed:               true,
		UserTurnPendingStart: queue != nil && queue.UserTurnPendingStart,
		RestoredPrompt:       restored,
	}
}

func NewSafetyBufferingPromptView(fasterModel string) SelectionView {
	return SelectionView{
		ViewID:      SafetyBufferingPromptViewID,
		Title:       "Additional safety checks",
		Subtitle:    SafetyBufferingMessageWithRetry,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              "retry",
				Name:            "Retry with a faster model",
				Description:     fasterModel,
				Action:          SafetyBufferingActionRetry,
				DismissOnSelect: true,
			},
			{
				ID:              "wait",
				Name:            "Keep waiting",
				Action:          SafetyBufferingActionWait,
				DismissOnSelect: true,
			},
		},
	}
}
