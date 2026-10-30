package rollout

import (
	"encoding/json"
	"strings"

	"codex_go/eventmap"
	"codex_go/retainedctx"
)

// retainedCallSource identifies the function call that produced a retained
// verified answer. A steer shares its turn ID with the instruction it replaces,
// so the call identity is what keeps the answer attached to the right
// instruction boundary.
type retainedCallSource struct {
	turnID string
	callID string
}

type retainedInstructionBoundary struct {
	turnID string
	alive  bool
}

// retainedRollbackPlan replays a rollout's instruction boundaries and function
// calls so retained answers can follow their source call across a rollback.
// Compaction does not remove a boundary; only ThreadRolledBack does.
type retainedRollbackPlan struct {
	boundaries   []retainedInstructionBoundary
	stack        []int
	calls        map[retainedCallSource]int
	removedTurns map[string]bool
	rolledBack   bool
}

func newRetainedRollbackPlan(lines []Line) *retainedRollbackPlan {
	plan := &retainedRollbackPlan{
		calls:        map[retainedCallSource]int{},
		removedTurns: map[string]bool{},
	}
	activeTurnID := ""
	for i := range lines {
		line := &lines[i]
		if line.ThreadRolledBack != nil {
			plan.rollback(int(line.ThreadRolledBack.NumTurns))
			continue
		}
		if line.Type == "event_msg" {
			event, ok := decodeAttributionEvent(line.Payload)
			if ok {
				switch event.kind {
				case "turn_started":
					activeTurnID = event.turnID
				case "turn_complete", "turn_aborted":
					if event.turnID == "" || activeTurnID == event.turnID {
						activeTurnID = ""
					}
				}
				continue
			}
			if item, turnID, ok := retainedItemCompletedPayload(line.Payload); ok {
				if turnID == "" {
					turnID = activeTurnID
				}
				if retainedCoreUserMessageIsInstruction(item) {
					plan.pushBoundary(turnID)
				}
				if callID := retainedCoreToolCallID(item); callID != "" {
					plan.recordCall(turnID, callID)
				}
			}
			continue
		}
		if line.Type != "item" {
			continue
		}
		turnID := retainedLineTurnID(line, activeTurnID)
		if rolloutLineCountsAsUserTurn(line) {
			plan.pushBoundary(turnID)
		}
		if callID := retainedFunctionCallID(line.Item); callID != "" {
			plan.recordCall(turnID, callID)
		}
	}
	return plan
}

func (p *retainedRollbackPlan) pushBoundary(turnID string) {
	index := len(p.boundaries)
	p.boundaries = append(p.boundaries, retainedInstructionBoundary{
		turnID: turnID,
		alive:  true,
	})
	p.stack = append(p.stack, index)
}

func (p *retainedRollbackPlan) recordCall(turnID string, callID string) {
	boundary := -1
	if len(p.stack) > 0 {
		boundary = p.stack[len(p.stack)-1]
	}
	p.calls[retainedCallSource{turnID: turnID, callID: callID}] = boundary
}

func (p *retainedRollbackPlan) rollback(numTurns int) {
	if p == nil || numTurns <= 0 {
		return
	}
	p.rolledBack = true
	for i := 0; i < numTurns && len(p.stack) > 0; i++ {
		boundaryIndex := p.stack[len(p.stack)-1]
		p.stack = p.stack[:len(p.stack)-1]
		p.boundaries[boundaryIndex].alive = false
		if turnID := p.boundaries[boundaryIndex].turnID; turnID != "" {
			p.removedTurns[turnID] = true
		}
		for source, boundary := range p.calls {
			if boundary == boundaryIndex && source.turnID != "" {
				p.removedTurns[source.turnID] = true
			}
		}
	}
}

// keepAnswer mirrors Rust's source-call retention: an answer whose exact
// function call survives the rollback stays, even when a later steer in the
// same turn was rolled back. Legacy answers without a recorded source call keep
// the conservative source-turn rule.
func (p *retainedRollbackPlan) keepAnswer(answer *retainedctx.VerifiedAnswer) bool {
	if p == nil || answer == nil {
		return false
	}
	if boundary, ok := p.calls[retainedCallSource{turnID: answer.TurnID, callID: answer.CallID}]; ok {
		if boundary < 0 {
			return !p.removedTurns[answer.TurnID]
		}
		return p.boundaries[boundary].alive
	}
	return !p.removedTurns[answer.TurnID]
}

func (p *retainedRollbackPlan) filterContext(context *retainedctx.RetainedContext) *retainedctx.RetainedContext {
	if context == nil || p == nil || !p.rolledBack {
		return context
	}
	filtered := context.Clone()
	filtered.RetainAnswers(p.keepAnswer)
	return filtered
}

func retainedLineTurnID(line *Line, activeTurnID string) string {
	if line == nil {
		return activeTurnID
	}
	if turnID := strings.TrimSpace(line.TurnID); turnID != "" {
		return turnID
	}
	if turnID := responseItemTurnID(line.Item); turnID != "" {
		return turnID
	}
	var raw struct {
		TurnID      string `json:"turn_id"`
		TurnIDCamel string `json:"turnId"`
	}
	if len(line.Item) > 0 && json.Unmarshal(line.Item, &raw) == nil {
		if turnID := firstNonEmptyString(raw.TurnID, raw.TurnIDCamel); turnID != "" {
			return turnID
		}
	}
	return activeTurnID
}

func retainedFunctionCallID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var item struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.Type != "function_call" {
		return ""
	}
	return strings.TrimSpace(item.CallID)
}

func retainedItemCompletedPayload(payload json.RawMessage) (json.RawMessage, string, bool) {
	if len(payload) == 0 {
		return nil, "", false
	}
	var event struct {
		Type        string          `json:"type"`
		TurnID      string          `json:"turn_id"`
		TurnIDCamel string          `json:"turnId"`
		Item        json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, "", false
	}
	if normalizeRolloutEventType(event.Type) != "item_completed" || len(event.Item) == 0 {
		return nil, "", false
	}
	return event.Item, firstNonEmptyString(event.TurnID, event.TurnIDCamel), true
}

func retainedCoreUserMessageIsInstruction(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var item struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return false
	}
	kind := normalizeRolloutItemType(item.Type)
	if kind != "message" {
		return false
	}
	if role := strings.TrimSpace(item.Role); role != "" && !strings.EqualFold(role, "user") {
		return false
	}
	content := make([]eventmap.ContentItem, 0, len(item.Content))
	for _, part := range item.Content {
		kind := strings.ToLower(strings.TrimSpace(part.Type))
		switch kind {
		case "text", "inputtext", "input_text":
			kind = string(eventmap.ContentInputText)
		}
		content = append(content, eventmap.ContentItem{Kind: eventmap.ContentKind(kind), Text: part.Text})
	}
	return !eventmap.IsContextualUserMessageContent(content)
}

func retainedCoreToolCallID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var item struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "dynamictoolcall", "dynamic_tool_call", "function_call", "custom_tool_call", "commandexecution", "command_execution", "mcptoolcall", "mcp_tool_call":
		return strings.TrimSpace(item.ID)
	default:
		return ""
	}
}
