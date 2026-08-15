package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/features"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

const (
	goalActionRead       = "read"
	goalActionPrepareSet = "prepare_set"
	goalActionSet        = "set"
	goalActionEditRead   = "edit_read"
	goalActionEdit       = "edit"
	goalActionPause      = "pause"
	goalActionResume     = "resume"
	goalActionClear      = "clear"
)

func (m *Model) applyGoalCommand(args string) bubbletea.Cmd {
	if m == nil || !features.Enabled(m.featureSettings, "goals") {
		m.pendingGoalDraft = nil
		return nil
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		m.pendingGoalDraft = nil
		return m.readGoal()
	}
	switch strings.ToLower(trimmed) {
	case goalActionClear:
		m.pendingGoalDraft = nil
		if _, ok := m.goalThreadIDForChange(); !ok {
			return nil
		}
		return m.clearGoal()
	case goalActionEdit:
		m.pendingGoalDraft = nil
		return m.editGoal()
	case goalActionPause:
		m.pendingGoalDraft = nil
		if _, ok := m.goalThreadIDForChange(); !ok {
			return nil
		}
		status := appserver.GoalPaused
		return m.setGoal(goalActionPause, nil, nil, &status, false)
	case goalActionResume:
		m.pendingGoalDraft = nil
		if _, ok := m.goalThreadIDForChange(); !ok {
			return nil
		}
		status := appserver.GoalActive
		return m.setGoal(goalActionResume, nil, nil, &status, false)
	default:
		return m.prepareGoalSet(trimmed)
	}
}

func (m *Model) readGoal() bubbletea.Cmd {
	threadID := m.goalThreadID()
	if threadID == "" {
		m.showGoalUsage("Example: /goal improve benchmark coverage")
		return nil
	}
	return m.readGoalForAction(goalActionRead, threadID, "")
}

func (m *Model) editGoal() bubbletea.Cmd {
	threadID := m.goalThreadID()
	if threadID == "" {
		m.showNoGoalToEdit()
		return nil
	}
	return m.readGoalForAction(goalActionEditRead, threadID, "")
}

func (m *Model) prepareGoalSet(objective string) bubbletea.Cmd {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		m.pendingGoalDraft = nil
		return nil
	}
	threadID := m.goalThreadID()
	if threadID == "" {
		m.pendingGoalObjective = objective
		m.notice = ""
		m.refreshTranscript()
		return nil
	}
	return m.readGoalForAction(goalActionPrepareSet, threadID, objective)
}

func (m *Model) readGoalForAction(action string, threadID string, objective string) bubbletea.Cmd {
	requestID := m.nextGoalRequest()
	m.pendingGoalRequestID = requestID
	reader := m.onReadGoal
	current := cloneGoalTea(m.currentGoal)
	return func() bubbletea.Msg {
		if reader == nil {
			return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Objective: objective, Goal: current}
		}
		goal, err := reader(threadID)
		return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Objective: objective, Goal: goal, Err: err}
	}
}

func (m *Model) setGoal(action string, objective *string, tokenBudget *int64, status *appserver.GoalStatus, replacing bool) bubbletea.Cmd {
	threadID := m.goalThreadID()
	if threadID == "" {
		m.pendingGoalDraft = nil
		return nil
	}
	requestID := m.nextGoalRequest()
	m.pendingGoalRequestID = requestID
	objective = cloneStringPtrTea(objective)
	tokenBudget = cloneInt64PtrTea(tokenBudget)
	status = cloneGoalStatusPtr(status)
	if m.pendingGoalDraft != nil && m.onGoalDraftMaterialize != nil {
		draft := *m.pendingGoalDraft
		m.pendingGoalDraft = nil
		materializer := m.onGoalDraftMaterialize
		return func() bubbletea.Msg {
			materialized, err := materializer(draft)
			return GoalDraftMaterializeMsg{
				RequestID:   requestID,
				Action:      action,
				ThreadID:    threadID,
				Objective:   materialized,
				TokenBudget: tokenBudget,
				Status:      status,
				Replacing:   replacing,
				Err:         err,
			}
		}
	}
	m.pendingGoalDraft = nil
	setter := m.onSetGoal
	clearer := m.onClearGoal
	current := cloneGoalTea(m.currentGoal)
	return func() bubbletea.Msg {
		if replacing && clearer != nil {
			if _, err := clearer(threadID); err != nil {
				return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Objective: stringPtrValueTea(objective), Replacing: true, Err: err}
			}
		}
		if setter != nil {
			goal, err := setter(threadID, objective, tokenBudget, status)
			return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Objective: stringPtrValueTea(objective), Goal: &goal, Replacing: replacing, Err: err}
		}
		goal := appserver.Goal{ThreadID: threadID, Status: appserver.GoalActive}
		if !replacing && current != nil {
			goal = *current
		}
		if objective != nil {
			goal.Objective = strings.TrimSpace(*objective)
		}
		if tokenBudget != nil {
			goal.TokenBudget = cloneInt64PtrTea(tokenBudget)
		}
		if status != nil {
			goal.Status = *status
		}
		if strings.TrimSpace(goal.Objective) == "" {
			return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Replacing: replacing, Err: appserver.ErrInvalidThreadExtraRequest}
		}
		return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Objective: stringPtrValueTea(objective), Goal: &goal, Replacing: replacing}
	}
}

func (m *Model) clearGoal() bubbletea.Cmd {
	threadID := m.goalThreadID()
	if threadID == "" {
		return nil
	}
	requestID := m.nextGoalRequest()
	m.pendingGoalRequestID = requestID
	clearer := m.onClearGoal
	hadGoal := m.currentGoal != nil
	return func() bubbletea.Msg {
		if clearer == nil {
			return GoalResultMsg{RequestID: requestID, Action: goalActionClear, ThreadID: threadID, Cleared: hadGoal}
		}
		cleared, err := clearer(threadID)
		return GoalResultMsg{RequestID: requestID, Action: goalActionClear, ThreadID: threadID, Cleared: cleared, Err: err}
	}
}

func (m *Model) applyGoalResult(msg GoalResultMsg) bubbletea.Cmd {
	if m == nil || m.pendingGoalRequestID != msg.RequestID {
		return nil
	}
	m.pendingGoalRequestID = 0
	if msg.Err != nil {
		action := "read"
		switch msg.Action {
		case goalActionPause, goalActionResume:
			action = "update"
		case goalActionClear:
			action = "clear"
		case goalActionSet, goalActionEdit:
			action = "set"
			if msg.Replacing {
				action = "replace"
			}
		}
		m.showGoalError(tuiapp.ThreadGoalErrorMessage(action, msg.Err))
		return nil
	}

	switch msg.Action {
	case goalActionRead:
		if msg.Goal == nil {
			m.applyGoalCleared(msg.ThreadID, false)
			m.showGoalUsage("No goal is currently set.")
			return nil
		}
		m.applyGoalUpdated(*msg.Goal, false)
		m.showGoal(msg.Goal)
	case goalActionEditRead:
		if msg.Goal == nil {
			m.showNoGoalToEdit()
			return nil
		}
		m.applyGoalUpdated(*msg.Goal, false)
		if m.onGoalEditText != nil {
			return m.resolveGoalEditText(*msg.Goal)
		}
		m.openGoalEditPrompt(*msg.Goal)
	case goalActionPrepareSet:
		decision := tuiapp.SetThreadGoalDraftPreflightDecision(true, msg.Goal, nil, tuiapp.ThreadGoalSetConfirmIfExists, "", nil)
		if decision.ShowReplaceConfirmation {
			m.openGoalReplaceConfirmation(msg.Objective)
			return nil
		}
		status := appserver.GoalActive
		return m.setGoal(goalActionSet, &msg.Objective, nil, &status, decision.ReplacingGoal)
	case goalActionClear:
		if msg.Cleared {
			m.applyGoalCleared(msg.ThreadID, false)
		}
		decision := tuiapp.ThreadGoalClearDecision(true, msg.Cleared, nil)
		m.showGoalInfo(decision.InfoMessage, decision.Hint)
	case goalActionSet, goalActionEdit:
		if msg.Goal == nil {
			return nil
		}
		m.applyGoalUpdated(*msg.Goal, false)
		decision := tuiapp.ThreadGoalSetSuccessDecision(true, *msg.Goal)
		m.showGoalInfo(decision.InfoMessage, decision.Hint)
	case goalActionPause, goalActionResume:
		if msg.Goal == nil {
			return nil
		}
		m.applyGoalUpdated(*msg.Goal, false)
		decision := tuiapp.ThreadGoalStatusUpdateDecision(true, msg.Goal, nil)
		m.showGoalInfo(decision.InfoMessage, decision.Hint)
	}
	return nil
}

func (m *Model) applyGoalDraftMaterializeResult(msg GoalDraftMaterializeMsg) bubbletea.Cmd {
	if m == nil || m.pendingGoalRequestID != msg.RequestID {
		return nil
	}
	m.pendingGoalRequestID = 0
	if msg.Err != nil {
		m.showGoalError(msg.Err.Error())
		return nil
	}
	return m.setGoal(msg.Action, &msg.Objective, msg.TokenBudget, msg.Status, msg.Replacing)
}

func (m *Model) openGoalReplaceConfirmation(objective string) {
	view := tuiapp.ReplaceThreadGoalConfirmation(m.goalThreadID(), objective)
	m.modal = &modalState{
		id:            "replace-goal",
		kind:          ModalKindGoal,
		title:         view.Title,
		body:          view.Subtitle,
		footerHint:    view.FooterHint,
		goalObjective: objective,
		options: []ModalOption{
			{ID: "replace", Label: view.ReplaceName, Description: view.ReplaceHint},
			{ID: "cancel", Label: view.CancelName, Description: view.CancelHint},
		},
	}
	m.notice = ""
}

// resolveGoalEditText loads the materialized goal objective file (when the
// stored objective is a managed goal file reference) before opening the edit
// prompt, mirroring Rust open_thread_goal_editor -> objective_text_for_edit.
func (m *Model) resolveGoalEditText(goal appserver.Goal) bubbletea.Cmd {
	resolver := m.onGoalEditText
	return func() bubbletea.Msg {
		text, err := resolver(goal.ThreadID, goal.Objective)
		return GoalEditTextMsg{ThreadID: goal.ThreadID, Objective: text, Goal: goal, Err: err}
	}
}

func (m *Model) applyGoalEditTextResult(msg GoalEditTextMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if msg.Err != nil {
		m.showGoalError(msg.Err.Error())
		return nil
	}
	goal := cloneGoalTea(&msg.Goal)
	goal.Objective = msg.Objective
	m.openGoalEditPrompt(*goal)
	return nil
}

func (m *Model) applyGoalModalOption(optionID string, objective string) bubbletea.Cmd {
	if strings.TrimSpace(optionID) != "replace" {
		return nil
	}
	status := appserver.GoalActive
	return m.setGoal(goalActionSet, &objective, nil, &status, true)
}

func (m *Model) openGoalEditPrompt(goal appserver.Goal) {
	view := chatwidget.NewGoalEditPromptView(goal)
	prompt := bottompane.NewCustomPromptView(view.Title, view.Placeholder, view.InitialText, "")
	m.modal = &modalState{
		id:           "edit-goal",
		kind:         ModalKindGoal,
		customPrompt: prompt,
		customPromptSubmit: func(objective string) bubbletea.Cmd {
			return m.setGoal(goalActionEdit, &objective, view.TokenBudget, &view.Status, false)
		},
	}
	m.notice = ""
}

func (m *Model) showGoal(goal *appserver.Goal) {
	if m == nil || m.State == nil || goal == nil {
		return
	}
	lines := chatwidget.GoalSummaryLines(*goal)
	m.State.AddHistoryLines(lines, lines)
	m.notice = ""
	m.refreshTranscript()
}

func (m *Model) showGoalUsage(hint string) {
	m.showGoalInfo(tuiapp.ThreadGoalUsageMessage, hint)
}

func (m *Model) showNoGoalToEdit() {
	m.showGoalError("No goal is currently set.")
	m.showGoalUsage("Create a goal before editing it.")
}

func (m *Model) showGoalInfo(message string, hint string) {
	if m == nil {
		return
	}
	m.addHistoryCell(historycell.NewInfoEvent(strings.TrimSpace(message), strings.TrimSpace(hint)))
	m.notice = strings.TrimSpace(message)
	m.refreshTranscript()
}

func (m *Model) showGoalError(message string) {
	if m == nil {
		return
	}
	m.addErrorHistoryMessage(message)
	m.notice = strings.TrimSpace(message)
	m.refreshTranscript()
}

func (m *Model) applyGoalUpdated(goal appserver.Goal, notify bool) {
	if m == nil {
		return
	}
	goal.ThreadID = strings.TrimSpace(goal.ThreadID)
	if goal.ThreadID == "" && m.State != nil {
		goal.ThreadID = strings.TrimSpace(m.State.ThreadID)
	}
	if m.State != nil && goal.ThreadID != "" {
		m.State.SetThreadID(goal.ThreadID)
	}
	m.currentGoal = cloneGoalTea(&goal)
	m.goalObservedAt = m.currentTime()
	if notify {
		m.notice = "Goal updated: " + chatwidget.GoalStatusLabel(goal.Status)
		m.refreshTranscript()
	}
}

func (m *Model) applyGoalCleared(threadID string, notify bool) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || m.currentGoal == nil || m.currentGoal.ThreadID == "" || m.currentGoal.ThreadID == threadID {
		m.currentGoal = nil
		m.goalObservedAt = m.currentTime()
	}
	if notify {
		m.notice = "Goal cleared"
		m.refreshTranscript()
	}
}

func (m *Model) goalThreadID() string {
	if m == nil || m.State == nil {
		return ""
	}
	return strings.TrimSpace(m.State.ThreadID)
}

func (m *Model) goalThreadIDForChange() (string, bool) {
	threadID := m.goalThreadID()
	if threadID == "" {
		m.showGoalUsage("The session must start before you can change a goal.")
		return "", false
	}
	return threadID, true
}

func (m *Model) goalTaskProgress() string {
	if m == nil || m.currentGoal == nil {
		return ""
	}
	indicator, ok := chatwidget.NewGoalStatusState(*m.currentGoal, m.goalObservedAt).Indicator(m.currentTime(), nil)
	if !ok {
		return ""
	}
	return "Goal " + chatwidget.FormatGoalStatusIndicator(indicator)
}

func (m *Model) nextGoalRequest() uint64 {
	if m == nil {
		return 0
	}
	m.nextGoalRequestID++
	if m.nextGoalRequestID == 0 {
		m.nextGoalRequestID = 1
	}
	return m.nextGoalRequestID
}

func cloneGoalTea(goal *appserver.Goal) *appserver.Goal {
	if goal == nil {
		return nil
	}
	clone := *goal
	clone.TokenBudget = cloneInt64PtrTea(goal.TokenBudget)
	return &clone
}

func cloneGoalStatusPtr(status *appserver.GoalStatus) *appserver.GoalStatus {
	if status == nil {
		return nil
	}
	clone := *status
	return &clone
}

func stringPtrValueTea(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
