package chatwidget

import "time"

type TurnLifecycleState struct {
	AgentTurnRunning                bool
	LastTurnID                      string
	LastErrorTurnID                 string
	LastErrorMessage                string
	PreventIdleSleep                bool
	SleepInhibitorTurnRunning       bool
	GoalStatusActiveTurnStartedAt   time.Time
	GoalStatusActiveTurnStartedAtOK bool
	BudgetLimitedTurnIDs            map[string]bool
}

func NewTurnLifecycleState(preventIdleSleep bool) TurnLifecycleState {
	return TurnLifecycleState{PreventIdleSleep: preventIdleSleep}
}

func (s *TurnLifecycleState) Start(turnID string) {
	s.StartAt(turnID, time.Now())
}

func (s *TurnLifecycleState) StartAt(turnID string, now time.Time) {
	if s == nil {
		return
	}
	s.AgentTurnRunning = true
	s.LastTurnID = turnID
	s.LastErrorTurnID = ""
	s.LastErrorMessage = ""
	s.GoalStatusActiveTurnStartedAt = now
	s.GoalStatusActiveTurnStartedAtOK = true
	s.SleepInhibitorTurnRunning = true
}

func (s *TurnLifecycleState) Complete(turnID string) bool {
	if s == nil {
		return false
	}
	if turnID != "" && s.LastTurnID != "" && turnID != s.LastTurnID {
		return false
	}
	s.Finish()
	return true
}

func (s *TurnLifecycleState) Finish() {
	if s == nil {
		return
	}
	s.AgentTurnRunning = false
	s.GoalStatusActiveTurnStartedAt = time.Time{}
	s.GoalStatusActiveTurnStartedAtOK = false
	s.SleepInhibitorTurnRunning = false
}

func (s *TurnLifecycleState) RestoreRunning(running bool, now time.Time) {
	if s == nil {
		return
	}
	s.AgentTurnRunning = running
	s.SleepInhibitorTurnRunning = running
	if running {
		s.GoalStatusActiveTurnStartedAt = now
		s.GoalStatusActiveTurnStartedAtOK = true
	} else {
		s.GoalStatusActiveTurnStartedAt = time.Time{}
		s.GoalStatusActiveTurnStartedAtOK = false
	}
}

func (s *TurnLifecycleState) ResetThread() {
	if s == nil {
		return
	}
	s.Finish()
	s.LastTurnID = ""
	s.BudgetLimitedTurnIDs = nil
}

func (s *TurnLifecycleState) SetPreventIdleSleep(enabled bool) {
	if s == nil {
		return
	}
	s.PreventIdleSleep = enabled
	s.SleepInhibitorTurnRunning = s.AgentTurnRunning
}

func (s *TurnLifecycleState) MarkBudgetLimited(turnID string) {
	if s == nil || turnID == "" {
		return
	}
	if s.BudgetLimitedTurnIDs == nil {
		s.BudgetLimitedTurnIDs = map[string]bool{}
	}
	s.BudgetLimitedTurnIDs[turnID] = true
}

func (s *TurnLifecycleState) TakeBudgetLimited(turnID string) bool {
	if s == nil || s.BudgetLimitedTurnIDs == nil || turnID == "" {
		return false
	}
	if !s.BudgetLimitedTurnIDs[turnID] {
		return false
	}
	delete(s.BudgetLimitedTurnIDs, turnID)
	return true
}

func (s *TurnLifecycleState) RecordError(turnID string, message string, willRetry bool) {
	if s == nil || willRetry {
		return
	}
	s.LastErrorTurnID = turnID
	s.LastErrorMessage = message
}

func (s TurnLifecycleState) IsCurrentTurn(turnID string) bool {
	return turnID == "" || s.LastTurnID == "" || turnID == s.LastTurnID
}
