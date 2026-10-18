package telemetry

import (
	"context"
	"strconv"
	"strings"

	"codex_go/eventmap"
)

const (
	// AgentResponseEventName is the opt-in log-only event for completed final
	// answers from main agents and thread-spawn subagents (Rust #47649).
	AgentResponseEventName = "codex.agent_response"
	// MaxAgentResponseBytes bounds exported response text at a UTF-8 boundary.
	MaxAgentResponseBytes = 64 * 1024
)

// AgentResponseEvent carries one completed final answer and its host-owned
// attribution. Response is the stored answer; memory citations are stripped
// only from the exported copy.
type AgentResponseEvent struct {
	AgentType            string
	TurnID               string
	ItemID               string
	ParentConversationID string
	ParentTurnID         string
	RootTurnID           string
	InitiatingAgentPath  string
	Response             string
}

// AgentResponse emits the opt-in log-only final-answer event. An empty or
// citation-only answer is not exported.
func (t *SessionTelemetry) AgentResponse(ctx context.Context, event AgentResponseEvent) {
	if t == nil {
		return
	}
	agentType := strings.TrimSpace(event.AgentType)
	turnID := strings.TrimSpace(event.TurnID)
	itemID := strings.TrimSpace(event.ItemID)
	if agentType == "" || turnID == "" || itemID == "" {
		return
	}
	text := eventmap.StripCitations(event.Response)
	if strings.TrimSpace(text) == "" {
		return
	}
	response := takeBytesAtCharBoundary(text, MaxAgentResponseBytes)
	fields := map[string]string{
		"agent.type": agentType,
		"turn.id":    turnID,
		"item.id":    itemID,
	}
	if value := strings.TrimSpace(event.ParentConversationID); value != "" {
		fields["parent.conversation.id"] = value
	}
	if value := strings.TrimSpace(event.ParentTurnID); value != "" {
		fields["parent.turn.id"] = value
	}
	if value := strings.TrimSpace(event.RootTurnID); value != "" {
		fields["root.turn.id"] = value
	}
	if value := strings.TrimSpace(event.InitiatingAgentPath); value != "" {
		fields["initiating.agent.path"] = value
	}
	logOnly := map[string]string{
		"response":           response,
		"response_length":    strconv.Itoa(len(text)),
		"response_truncated": strconv.FormatBool(len(response) < len(text)),
	}
	t.LogEvent(ctx, AgentResponseEventName, fields, logOnly)
}
