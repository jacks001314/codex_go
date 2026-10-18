package appserver

import (
	"context"
	"strings"
	"sync"

	"codex_go/model"
	"codex_go/telemetry"
)

// agentResponseRecorder mirrors Rust's AgentResponseLogger wiring (#47649):
// when otel.log_agent_responses is enabled with an OTLP log exporter, every
// completed final answer is exported once per turn. Review, compact, memory,
// and other internal agents are excluded.
func (r *RuntimeRouter) agentResponseRecorder(ctx context.Context, threadID string, turnID string, runConfig *appTurnRunConfig) func(model.AgentItem) {
	if r == nil || runConfig == nil || !runConfig.LogAgentResponses {
		return nil
	}
	agentType, parentConversationID, ok := agentResponseAttribution(runConfig)
	if !ok {
		return nil
	}
	sink := r.sessionTelemetryForThread(threadID)
	if sink == nil {
		return nil
	}
	var mu sync.Mutex
	logged := map[string]struct{}{}
	return func(item model.AgentItem) {
		if !agentResponseIsFinalAnswer(item) {
			return
		}
		itemID := strings.TrimSpace(item.ID)
		if itemID == "" {
			return
		}
		mu.Lock()
		if _, seen := logged[itemID]; seen {
			mu.Unlock()
			return
		}
		logged[itemID] = struct{}{}
		mu.Unlock()
		sink.AgentResponse(ctx, telemetry.AgentResponseEvent{
			AgentType:            agentType,
			TurnID:               turnID,
			ItemID:               itemID,
			ParentConversationID: parentConversationID,
			ParentTurnID:         runConfig.ParentTurnID,
			RootTurnID:           runConfig.RootTurnID,
			InitiatingAgentPath:  runConfig.InitiatingAgentPath,
			Response:             item.Text,
		})
	}
}

func agentResponseAttribution(runConfig *appTurnRunConfig) (string, string, bool) {
	if runConfig == nil {
		return "", "", false
	}
	source := strings.ToLower(strings.TrimSpace(runConfig.ThreadSource))
	switch {
	case source == "subagentthreadspawn":
		return "subagent", strings.TrimSpace(runConfig.ParentThreadID), true
	case strings.HasPrefix(source, "internal"),
		strings.Contains(source, "review"),
		strings.Contains(source, "compact"),
		strings.Contains(source, "memory"),
		strings.HasPrefix(source, "subagent"),
		strings.HasPrefix(source, "sub_agent"):
		return "", "", false
	case source == "" && strings.TrimSpace(runConfig.ParentThreadID) != "":
		return "subagent", strings.TrimSpace(runConfig.ParentThreadID), true
	default:
		return "main", "", true
	}
}

func agentResponseIsFinalAnswer(item model.AgentItem) bool {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "agent_message", "message", "assistant_message":
	default:
		return false
	}
	phase := strings.ToLower(strings.TrimSpace(stringValueFromMap(item.Data, "phase")))
	return phase == string(MessagePhaseFinalAnswer) && strings.TrimSpace(item.Text) != ""
}
