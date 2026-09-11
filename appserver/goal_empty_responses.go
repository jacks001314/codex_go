package appserver

import (
	"strings"

	"codex_go/model"
	"codex_go/turn"
)

// Goal empty-response accounting (Rust #44320): automatically admitted goal
// continuation turns that finish with an empty final answer and no activity
// are counted, and three consecutive empty continuations block the goal.

const goalEmptyResponseThreshold = 3

// markGoalContinuation records the turn admitted for an automatic goal
// continuation so the turn stop can attribute empty output to it.
func (r *RuntimeRouter) markGoalContinuation(threadID, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	state := r.emptyResponseTurns[threadID]
	state.AutomaticTurnID = turnID
	r.emptyResponseTurns[threadID] = state
}

// resetGoalEmptyResponses invalidates the automatic turn and clears the streak
// (Rust #44320 reset_empty_responses).
func (r *RuntimeRouter) resetGoalEmptyResponses(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	delete(r.emptyResponseTurns, threadID)
}

// recordGoalResultItems updates the active goal's turn snapshot from the
// finished turn's model result, mirroring Rust's per-item on_item_completed
// accounting: user/reasoning/tool/other items mark activity, and a
// non-commentary agent message with no text marks an empty final answer.
func (r *RuntimeRouter) recordGoalResultItems(threadID, turnID string, params *turn.TurnStartParams, result *turn.AgentLoopResult) {
	if r == nil || result == nil {
		return
	}
	key := stateGoalTurnKey(threadID, turnID)
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	snapshot, ok := r.goalAccountingTurns[key]
	if !ok || strings.TrimSpace(snapshot.GoalID) == "" {
		return
	}
	if goalTurnHasUserInput(params) {
		snapshot.HasActivity = true
	}
	for _, response := range result.ModelResponses() {
		if response == nil {
			continue
		}
		for index := range response.Items {
			goalItemActivity(&snapshot, &response.Items[index])
		}
	}
	r.goalAccountingTurns[key] = snapshot
}

func goalTurnHasUserInput(params *turn.TurnStartParams) bool {
	if params == nil {
		return false
	}
	if strings.TrimSpace(params.Prompt) != "" {
		return true
	}
	for _, input := range params.Input {
		if strings.TrimSpace(input.Text) != "" {
			return true
		}
	}
	return false
}

func goalItemActivity(snapshot *stateGoalTurnSnapshot, item *model.AgentItem) {
	if snapshot == nil || item == nil {
		return
	}
	switch goalAgentItemKind(item) {
	case goalItemReasoning:
		if strings.TrimSpace(item.Text) != "" || goalReasoningSummaryPresent(item) {
			snapshot.HasActivity = true
		}
	case goalItemAgentMessage:
		text := strings.TrimSpace(item.Text)
		if text != "" || goalAgentMessageHasQuestions(item) {
			snapshot.HasActivity = true
		} else if !strings.EqualFold(goalAgentMessagePhase(item), string(MessagePhaseCommentary)) {
			snapshot.EmptyFinal = true
		}
	default:
		snapshot.HasActivity = true
	}
}

type goalAgentItemKindValue uint8

const (
	goalItemOther goalAgentItemKindValue = iota
	goalItemAgentMessage
	goalItemReasoning
)

func goalAgentItemKind(item *model.AgentItem) goalAgentItemKindValue {
	if item == nil {
		return goalItemOther
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "reasoning":
		return goalItemReasoning
	case "message", "agent_message":
		return goalItemAgentMessage
	default:
		return goalItemOther
	}
}

func goalAgentMessagePhase(item *model.AgentItem) string {
	value := goalAgentItemDataValue(item, "phase", "messagePhase")
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func goalAgentMessageHasQuestions(item *model.AgentItem) bool {
	if item == nil || item.Data == nil {
		return false
	}
	value, ok := item.Data["questions"]
	if !ok || value == nil {
		return false
	}
	if list, ok := value.([]any); ok {
		return len(list) > 0
	}
	return true
}

func goalReasoningSummaryPresent(item *model.AgentItem) bool {
	if item == nil || item.Data == nil {
		return false
	}
	value, ok := item.Data["summary"]
	if !ok || value == nil {
		return false
	}
	if list, ok := value.([]any); ok {
		for _, entry := range list {
			if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
				return true
			}
			if table, ok := entry.(map[string]any); ok && strings.TrimSpace(stringFromMap(table, "text")) != "" {
				return true
			}
		}
		return false
	}
	text, _ := value.(string)
	return strings.TrimSpace(text) != ""
}

func goalAgentItemDataValue(item *model.AgentItem, keys ...string) any {
	if item == nil {
		return nil
	}
	return threadItemAnyFromData(item.Data, keys...)
}

// advanceGoalEmptyResponses evaluates the consecutive-empty-continuation streak
// for the just-finished turn and returns the active goal ID once the threshold
// is reached (Rust #44320 empty_response_goal).
func (r *RuntimeRouter) advanceGoalEmptyResponses(threadID, turnID, goalID string, snapshot stateGoalTurnSnapshot) string {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	goalID = strings.TrimSpace(goalID)
	if r == nil || threadID == "" || turnID == "" || goalID == "" {
		return ""
	}
	r.goalAccountingMu.Lock()
	defer r.goalAccountingMu.Unlock()
	state := r.emptyResponseTurns[threadID]
	if state.GoalID != goalID {
		state.GoalID = goalID
		state.Turns = 0
		// A replacement goal restarts the streak, but keep the marker when this
		// turn is the automatically admitted continuation being evaluated.
		if strings.TrimSpace(state.AutomaticTurnID) != turnID {
			state.AutomaticTurnID = ""
		}
	}
	automatic := strings.TrimSpace(state.AutomaticTurnID) == turnID
	empty := automatic && snapshot.EmptyFinal && !snapshot.HasActivity
	if !empty {
		state.Turns = 0
		state.AutomaticTurnID = ""
		r.emptyResponseTurns[threadID] = state
		return ""
	}
	state.Turns++
	r.emptyResponseTurns[threadID] = state
	if state.Turns >= goalEmptyResponseThreshold {
		return goalID
	}
	return ""
}
