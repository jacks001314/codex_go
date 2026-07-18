package tea

import (
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	chatwidget "codex_go/tui/chatwidget"
)

const (
	goalActionRead   = "read"
	goalActionSet    = "set"
	goalActionEdit   = "edit"
	goalActionPause  = "pause"
	goalActionResume = "resume"
	goalActionClear  = "clear"
)

func (m *Model) applyGoalCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	action, rest := splitGoalCommandArgs(args)
	switch action {
	case "", "show", "status":
		return m.readGoal()
	case goalActionSet, "start":
		objective, budget, ok := parseGoalObjectiveAndBudget(rest)
		if !ok {
			m.notice = "Usage: /goal set OBJECTIVE [--budget TOKENS]"
			return nil
		}
		status := appserver.GoalActive
		return m.setGoal(goalActionSet, &objective, budget, &status)
	case goalActionEdit:
		objective, budget, ok := parseGoalObjectiveAndBudget(rest)
		if !ok {
			m.notice = "Usage: /goal edit OBJECTIVE [--budget TOKENS]"
			return nil
		}
		status := appserver.GoalActive
		if m.currentGoal != nil {
			status = chatwidget.EditedGoalStatus(m.currentGoal.Status)
		}
		return m.setGoal(goalActionEdit, &objective, budget, &status)
	case goalActionPause:
		status := appserver.GoalPaused
		return m.setGoal(goalActionPause, nil, nil, &status)
	case goalActionResume:
		status := appserver.GoalActive
		return m.setGoal(goalActionResume, nil, nil, &status)
	case goalActionClear, "delete":
		return m.clearGoal()
	default:
		m.notice = "Usage: /goal [status|set|edit|pause|resume|clear]"
		m.refreshTranscript()
		return nil
	}
}

func splitGoalCommandArgs(args string) (string, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", ""
	}
	action := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	return action, rest
}

func parseGoalObjectiveAndBudget(args string) (string, *int64, bool) {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return "", nil, false
	}
	objectiveParts := make([]string, 0, len(fields))
	var budget *int64
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "--budget" || field == "-b" {
			if i+1 >= len(fields) {
				return "", nil, false
			}
			parsed, err := strconv.ParseInt(strings.ReplaceAll(fields[i+1], "_", ""), 10, 64)
			if err != nil || parsed <= 0 {
				return "", nil, false
			}
			budget = &parsed
			i++
			continue
		}
		if strings.HasPrefix(field, "--budget=") {
			raw := strings.TrimPrefix(field, "--budget=")
			parsed, err := strconv.ParseInt(strings.ReplaceAll(raw, "_", ""), 10, 64)
			if err != nil || parsed <= 0 {
				return "", nil, false
			}
			budget = &parsed
			continue
		}
		objectiveParts = append(objectiveParts, field)
	}
	objective := strings.TrimSpace(strings.Join(objectiveParts, " "))
	return objective, budget, objective != ""
}

func (m *Model) readGoal() bubbletea.Cmd {
	threadID, ok := m.goalThreadID()
	if !ok {
		return nil
	}
	if m.onReadGoal == nil {
		m.showGoal(m.currentGoal, "Goal")
		return nil
	}
	requestID := m.nextGoalRequest()
	m.pendingGoalRequestID = requestID
	m.notice = "Loading goal..."
	m.refreshTranscript()
	return func() bubbletea.Msg {
		goal, err := m.onReadGoal(threadID)
		return GoalResultMsg{RequestID: requestID, Action: goalActionRead, ThreadID: threadID, Goal: goal, Err: err}
	}
}

func (m *Model) setGoal(action string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) bubbletea.Cmd {
	threadID, ok := m.goalThreadID()
	if !ok {
		return nil
	}
	if m.onSetGoal == nil {
		goal := m.localGoal(threadID)
		if objective != nil {
			goal.Objective = strings.TrimSpace(*objective)
		}
		if tokenBudget != nil {
			goal.TokenBudget = cloneInt64PtrTea(tokenBudget)
		}
		if status != nil {
			goal.Status = *status
		}
		if goal.Objective == "" {
			m.notice = "Goal objective is required."
			m.refreshTranscript()
			return nil
		}
		m.applyGoalUpdated(goal, false)
		m.showGoal(m.currentGoal, goalNotice(action))
		return nil
	}
	requestID := m.nextGoalRequest()
	m.pendingGoalRequestID = requestID
	m.notice = goalLoadingNotice(action)
	m.refreshTranscript()
	objective = cloneStringPtrTea(objective)
	tokenBudget = cloneInt64PtrTea(tokenBudget)
	status = cloneGoalStatusPtr(status)
	return func() bubbletea.Msg {
		goal, err := m.onSetGoal(threadID, objective, tokenBudget, status)
		if err != nil {
			return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Err: err}
		}
		return GoalResultMsg{RequestID: requestID, Action: action, ThreadID: threadID, Goal: &goal}
	}
}

func (m *Model) clearGoal() bubbletea.Cmd {
	threadID, ok := m.goalThreadID()
	if !ok {
		return nil
	}
	if m.onClearGoal == nil {
		m.applyGoalCleared(threadID, false)
		m.showGoal(nil, "Cleared goal")
		return nil
	}
	requestID := m.nextGoalRequest()
	m.pendingGoalRequestID = requestID
	m.notice = "Clearing goal..."
	m.refreshTranscript()
	return func() bubbletea.Msg {
		cleared, err := m.onClearGoal(threadID)
		return GoalResultMsg{RequestID: requestID, Action: goalActionClear, ThreadID: threadID, Cleared: cleared, Err: err}
	}
}

func (m *Model) applyGoalResult(msg GoalResultMsg) {
	if m == nil || m.pendingGoalRequestID != msg.RequestID {
		return
	}
	m.pendingGoalRequestID = 0
	if msg.Err != nil {
		m.notice = "Goal error: " + strings.TrimSpace(msg.Err.Error())
		m.refreshTranscript()
		return
	}
	if msg.Action == goalActionClear {
		if msg.Cleared {
			m.applyGoalCleared(msg.ThreadID, false)
		}
		m.showGoal(nil, "Cleared goal")
		return
	}
	if msg.Goal != nil {
		m.applyGoalUpdated(*msg.Goal, false)
	} else if msg.Action == goalActionRead {
		m.applyGoalCleared(msg.ThreadID, false)
	}
	m.showGoal(msg.Goal, goalNotice(msg.Action))
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

func (m *Model) showGoal(goal *appserver.Goal, title string) {
	if m == nil {
		return
	}
	lines := []string{strings.TrimSpace(title)}
	if lines[0] == "" {
		lines[0] = "Goal"
	}
	if goal == nil {
		lines = append(lines, "No goal set.", "", "Commands: /goal set OBJECTIVE [--budget TOKENS]")
	} else {
		lines = chatwidget.GoalSummaryLines(*goal)
		if strings.TrimSpace(title) != "" && strings.TrimSpace(title) != "Goal" {
			lines = append([]string{strings.TrimSpace(title), ""}, lines...)
		}
	}
	m.State.AddHistoryLines(lines, lines)
	m.notice = strings.TrimSpace(title)
	if m.notice == "" {
		m.notice = "Goal"
	}
	m.refreshTranscript()
}

func (m *Model) goalThreadID() (string, bool) {
	if m == nil || m.State == nil {
		return "", false
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		m.notice = "Goal requires an active thread."
		m.refreshTranscript()
		return "", false
	}
	return threadID, true
}

func (m *Model) localGoal(threadID string) appserver.Goal {
	if m != nil && m.currentGoal != nil {
		goal := *m.currentGoal
		goal.ThreadID = strings.TrimSpace(firstNonEmpty(goal.ThreadID, threadID))
		if goal.Status == "" {
			goal.Status = appserver.GoalActive
		}
		return goal
	}
	now := int64(0)
	if m != nil {
		now = m.currentTime().Unix()
	}
	return appserver.Goal{
		ThreadID:  strings.TrimSpace(threadID),
		Status:    appserver.GoalActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
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

func goalLoadingNotice(action string) string {
	switch action {
	case goalActionPause:
		return "Pausing goal..."
	case goalActionResume:
		return "Resuming goal..."
	case goalActionEdit:
		return "Updating goal..."
	default:
		return "Setting goal..."
	}
}

func goalNotice(action string) string {
	switch action {
	case goalActionPause:
		return "Paused goal"
	case goalActionResume:
		return "Resumed goal"
	case goalActionEdit:
		return "Updated goal"
	case goalActionSet:
		return "Set goal"
	default:
		return "Goal"
	}
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
