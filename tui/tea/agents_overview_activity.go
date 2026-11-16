package tea

import (
	"strings"

	"codex_go/protocol"
	agentsoverview "codex_go/tui/agents_overview"
)

// In-session live task details (Rust #42455). Delivered events update the
// command center's bounded previews without attaching to another thread or
// fetching additional history.

// trackAgentsOverviewActivity folds one delivered thread event into the
// dashboard's per-task previews and reports whether anything changed.
func (m *Model) trackAgentsOverviewActivity(event protocol.ThreadEvent) bool {
	if m == nil || m.agentsOverview == nil {
		return false
	}
	threadID := strings.TrimSpace(event.ThreadID)
	if threadID == "" {
		return false
	}
	switch event.Type {
	case "item.reasoning.delta":
		delta := ""
		itemID := ""
		if event.Delta != nil {
			delta = event.Delta.Text
			itemID = event.Delta.ItemID
		}
		return m.agentsOverview.TrackAgentsOverviewActivity(threadID, agentsoverview.AgentsOverviewEvent{
			Kind:   agentsoverview.AgentsOverviewEventReasoningDelta,
			TurnID: event.TurnID,
			ItemID: itemID,
			Delta:  delta,
		})
	case "item.completed":
		if event.Item == nil {
			return false
		}
		switch event.Item.Type {
		case "user_message":
			// Rust #61709: a legacy thread's prompt preview comes from the
			// completed user-message notification instead of a rollout replay.
			return m.agentsOverview.TrackAgentsOverviewActivity(threadID, agentsoverview.AgentsOverviewEvent{
				Kind: agentsoverview.AgentsOverviewEventUserMessage,
				Text: event.Item.Text,
			})
		case "agent_message":
		default:
			return false
		}
		return m.agentsOverview.TrackAgentsOverviewActivity(threadID, agentsoverview.AgentsOverviewEvent{
			Kind: agentsoverview.AgentsOverviewEventAgentMessage,
			Text: event.Item.Text,
		})
	case "turn.started":
		return m.agentsOverview.TrackAgentsOverviewActivity(threadID, agentsoverview.AgentsOverviewEvent{
			Kind: agentsoverview.AgentsOverviewEventTurnStarted,
		})
	case "turn.completed", "turn.failed", "error":
		return m.agentsOverview.TrackAgentsOverviewActivity(threadID, agentsoverview.AgentsOverviewEvent{
			Kind: agentsoverview.AgentsOverviewEventTurnCompleted,
		})
	case "item.started":
		return m.agentsOverview.TrackAgentsOverviewActivity(threadID, agentsoverview.AgentsOverviewEvent{
			Kind: agentsoverview.AgentsOverviewEventItemStarted,
		})
	}
	return false
}
