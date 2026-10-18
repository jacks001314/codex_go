package chatwidget

import "strings"

const (
	SafetyBufferingPromptViewID     = "safety-buffering-prompt"
	SafetyBufferingHeader           = "Giving this request a little extra thought"
	SafetyBufferingMessageWithRetry = "If you'd rather not wait, retry with a faster model. It may be less capable of handling complex requests."
	SafetyBufferingMessageNoRetry   = SafetyBufferingHeader
	SafetyBufferingFooter           = "No action is required. Codex will keep waiting, and this menu will close when the response is ready."
	SafetyBufferingLearnMoreURL     = "https://help.openai.com/en/articles/20001326"
)

const (
	SafetyBufferingActionRetry     UsageMenuAction = "safety_buffering_retry"
	SafetyBufferingActionWait      UsageMenuAction = "safety_buffering_wait"
	SafetyBufferingActionStopRetry UsageMenuAction = "safety_buffering_stop_retry"
	SafetyBufferingActionLearnMore UsageMenuAction = "safety_buffering_learn_more"
)

type SafetyBufferingState struct {
	SubmittedTurnID     string
	ActiveTurnID        string
	RetryPromptShown    bool
	RetryPromptHadRetry bool
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
	s.RetryPromptHadRetry = false
	s.AgentMessageStarted = false
	return SafetyBufferingResult{DismissPrompt: true, Cleared: true}
}

// MarkAgentMessageStarted records the first visible response text and reports
// whether the buffering UI should now close.
func (s *SafetyBufferingState) MarkAgentMessageStarted() bool {
	if s == nil || s.ActiveTurnID == "" || s.AgentMessageStarted {
		return false
	}
	s.AgentMessageStarted = true
	return true
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
	if s.AgentMessageStarted && s.ActiveTurnID == update.TurnID {
		return SafetyBufferingResult{Ignored: true}
	}
	if !update.ShowBufferingUI {
		if s.ActiveTurnID == update.TurnID {
			s.ActiveTurnID = ""
			s.RetryPromptShown = false
			s.RetryPromptHadRetry = false
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
	shouldShowPrompt := !s.RetryPromptShown || s.RetryPromptHadRetry != canRetry
	if shouldShowPrompt {
		s.RetryPromptShown = true
		s.RetryPromptHadRetry = canRetry
	}
	s.ActiveTurnID = update.TurnID
	message := SafetyBufferingMessageNoRetry
	if canRetry {
		message = SafetyBufferingHeader + " " + SafetyBufferingMessageWithRetry
	}
	result := SafetyBufferingResult{
		Status: StatusIndicatorState{
			Header:          "Working",
			Details:         message,
			DetailsMaxLines: 6,
		},
		Waiting:        !s.AgentMessageStarted,
		RetryAvailable: canRetry,
		Redraw:         true,
	}
	if shouldShowPrompt {
		view := NewSafetyBufferingPromptView(update.FasterModel, canRetry)
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

// NewSafetyBufferingPromptView builds the buffering prompt with the retry entry
// only when the server selected an eligible faster model.
func NewSafetyBufferingPromptView(fasterModel string, retryAvailable bool) SelectionView {
	view := SelectionView{
		ViewID:      SafetyBufferingPromptViewID,
		Title:       SafetyBufferingHeader,
		AllowCancel: true,
		FooterNote:  SafetyBufferingFooter,
	}
	if retryAvailable {
		view.Subtitle = SafetyBufferingMessageWithRetry
		view.Items = append(view.Items, SelectionItem{
			ID:              "retry",
			Name:            "Retry with a faster model",
			Action:          SafetyBufferingActionRetry,
			DismissOnSelect: true,
		})
	}
	view.Items = append(view.Items,
		SelectionItem{
			ID:              "wait",
			Name:            "Dismiss and keep waiting",
			Action:          SafetyBufferingActionWait,
			DismissOnSelect: true,
		},
		SelectionItem{
			ID:     "learn-more",
			Name:   "Learn more",
			Action: SafetyBufferingActionLearnMore,
		},
	)
	return view
}

// NewSafetyBufferingConfirmationView shows the Rust #42380 confirmation before
// a safety-buffered retry: it explains that retrying starts a new thread and
// re-sends the user's message with the selected faster model.
func NewSafetyBufferingConfirmationView(fasterModel string) SelectionView {
	modelLabel := strings.TrimSpace(fasterModel)
	if modelLabel == "" {
		modelLabel = "a faster model"
	}
	return SelectionView{
		ViewID:      SafetyBufferingPromptViewID,
		Title:       "Stop this attempt and retry?",
		Subtitle:    "This will stop the current attempt and retry in a new thread. Any file changes or other actions already taken will remain.\n\nYour message will be sent again using " + modelLabel + ", which may be less capable on complex tasks.",
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              "wait",
				Name:            "Keep waiting",
				Action:          SafetyBufferingActionWait,
				DismissOnSelect: true,
			},
			{
				ID:              "stop-retry",
				Name:            "Stop and retry",
				Action:          SafetyBufferingActionStopRetry,
				DismissOnSelect: true,
			},
		},
	}
}
