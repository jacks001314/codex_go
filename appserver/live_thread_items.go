package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// openLiveThreadItems starts the fork-visible overlay for a running app-server
// turn. Rust records completed conversation items into the live thread store as
// they arrive; the Go runtime persists the turn in one batch at its boundary,
// so this overlay supplies the in-flight suffix to fork readers.
func (r *RuntimeRouter) openLiveThreadItems(threadID string, turnID string) *session.LiveThreadItems {
	if r == nil || r.services.ThreadRouter == nil || r.services.ThreadRouter.store == nil {
		return nil
	}
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" || r.threadIsEphemeral(threadID) {
		return nil
	}
	return session.OpenLiveThreadItems(r.services.ThreadRouter.store, session.ThreadID(threadID), turnID)
}

func (r *RuntimeRouter) liveResponseItemRecorder(live *session.LiveThreadItems, turnID string, next func(model.AgentItem)) func(model.AgentItem) {
	if live == nil {
		return next
	}
	return func(item model.AgentItem) {
		r.recordLiveResponseItem(live, turnID, item)
		if next != nil {
			next(item)
		}
	}
}

// recordLiveResponseItem records the completed response items that the final
// turn persistence stores directly. Tool calls are recorded by
// liveToolCompletedRecorder with the same representation the final turn uses,
// so a fork never sees a half-formed call.
func (r *RuntimeRouter) recordLiveResponseItem(live *session.LiveThreadItems, turnID string, item model.AgentItem) {
	if live == nil {
		return
	}
	itemType := firstNonEmpty(strings.TrimSpace(item.Type), "agent_message")
	if itemType == "message" {
		itemType = "agent_message"
	}
	switch itemType {
	case "function_call", "custom_tool_call", "tool_search_call", "local_shell_call",
		"image_generation_call", "compaction", "context_compaction":
		return
	case "web_search_call":
		action, query := appWebSearchActionFromAgentItem(&item)
		raw, _ := json.Marshal(&item)
		live.Append(session.Item{
			ID:       firstNonEmpty(item.ID, item.CallID, "web-search-"+safeIdentifier(turnID)),
			Type:     "webSearch",
			Text:     query,
			Data:     map[string]any{"query": query, "action": action},
			Metadata: appTurnMetadata(turnID, nil),
			Raw:      raw,
		})
		return
	}
	if strings.TrimSpace(item.Text) == "" && itemType != "reasoning" {
		return
	}
	role := "assistant"
	if itemType == "reasoning" {
		role = ""
	}
	raw, _ := json.Marshal(&item)
	live.Append(session.Item{
		ID:       firstNonEmpty(item.ID, item.CallID, "assistant-"+strings.TrimPrefix(turnID, "turn-")),
		Type:     itemType,
		Role:     role,
		Text:     item.Text,
		Data:     cloneAnyMap(item.Data),
		Metadata: appTurnMetadata(turnID, nil),
		Raw:      raw,
	})
}

func (r *RuntimeRouter) liveToolCompletedRecorder(live *session.LiveThreadItems, turnID string, next turn.ToolCompletedCallback) turn.ToolCompletedCallback {
	if live == nil {
		return next
	}
	return func(ctx context.Context, execution *turn.ToolExecutionResult) {
		r.recordLiveToolExecution(live, turnID, execution)
		if next != nil {
			next(ctx, execution)
		}
	}
}

func (r *RuntimeRouter) recordLiveToolExecution(live *session.LiveThreadItems, turnID string, execution *turn.ToolExecutionResult) {
	if live == nil || execution == nil || execution.Invocation == nil {
		return
	}
	createdAt := execution.FinishedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	live.Append(sessionItemsForAppToolExecution(turnID, execution, createdAt)...)
}
