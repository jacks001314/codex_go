package app

import "strings"

const (
	AgentStatusPreviewLines     = 3
	AgentStatusPreviewItems     = 6
	AgentStatusPreviewGraphemes = 240
)

type AgentStatusFeedItem struct {
	AgentID string
	Status  string
}

type AgentActivityEvent struct {
	EventType string
	Item      AgentActivityItem
}

type AgentActivityItem struct {
	ID                 string
	Kind               string
	Text               string
	Command            string
	Changes            int
	Server             string
	Tool               string
	Namespace          string
	CollabTool         string
	SubAgentActivity   string
	AgentPath          string
	Query              string
	Path               string
	ReasoningSummaries []string
	ImageGenerated     bool
	EnteredReviewMode  bool
	ExitedReviewMode   bool
	ContextCompaction  bool
}

type AgentStatusThreadPreview struct {
	AgentPath string
	Activity  []string
}

const (
	AgentActivityEventItemStarted   = "item_started"
	AgentActivityEventItemCompleted = "item_completed"

	AgentActivityAgentMessage      = "agent_message"
	AgentActivityPlan              = "plan"
	AgentActivityReasoning         = "reasoning"
	AgentActivityCommandExecution  = "command_execution"
	AgentActivityFileChange        = "file_change"
	AgentActivityMcpToolCall       = "mcp_tool_call"
	AgentActivityDynamicToolCall   = "dynamic_tool_call"
	AgentActivityCollabToolCall    = "collab_agent_tool_call"
	AgentActivitySubAgentActivity  = "sub_agent_activity"
	AgentActivityWebSearch         = "web_search"
	AgentActivityImageView         = "image_view"
	AgentActivityImageGeneration   = "image_generation"
	AgentActivityEnteredReviewMode = "entered_review_mode"
	AgentActivityExitedReviewMode  = "exited_review_mode"
	AgentActivityContextCompaction = "context_compaction"

	CollabToolSpawnAgent  = "spawn_agent"
	CollabToolSendInput   = "send_input"
	CollabToolResumeAgent = "resume_agent"
	CollabToolWait        = "wait"
	CollabToolCloseAgent  = "close_agent"

	SubAgentActivityStarted     = "started"
	SubAgentActivityInteracted  = "interacted"
	SubAgentActivityInterrupted = "interrupted"
)

func NewAgentStatusThreadPreview(agentPath string, events []AgentActivityEvent) AgentStatusThreadPreview {
	seenItemIDs := map[string]struct{}{}
	activity := []string{}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.EventType != AgentActivityEventItemStarted && event.EventType != AgentActivityEventItemCompleted {
			continue
		}
		if event.Item.ID != "" {
			if _, ok := seenItemIDs[event.Item.ID]; ok {
				continue
			}
			seenItemIDs[event.Item.ID] = struct{}{}
		}
		if summary, ok := AgentActivitySummary(event.Item); ok {
			activity = append(activity, summary)
			if len(activity) == AgentStatusPreviewItems {
				break
			}
		}
	}
	reverseStrings(activity)
	return AgentStatusThreadPreview{AgentPath: agentPath, Activity: activity}
}

func (p AgentStatusThreadPreview) PreviewLines(width int) []string {
	lines := []string{}
	for _, activity := range p.Activity {
		for _, line := range wrapWords(activity, width) {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	if len(lines) > AgentStatusPreviewLines {
		lines = append([]string(nil), lines[len(lines)-AgentStatusPreviewLines:]...)
	}
	return lines
}

func AgentActivitySummary(item AgentActivityItem) (string, bool) {
	switch item.Kind {
	case AgentActivityAgentMessage, AgentActivityPlan:
		return BoundedAgentActivitySummary(item.Text)
	case AgentActivityReasoning:
		if len(item.ReasoningSummaries) == 0 {
			return "", false
		}
		return BoundedAgentActivitySummary(item.ReasoningSummaries[len(item.ReasoningSummaries)-1])
	case AgentActivityCommandExecution:
		return BoundedAgentActivitySummary("$ " + truncateRunesApp(item.Command, AgentStatusPreviewGraphemes-len("$ ")))
	case AgentActivityFileChange:
		return BoundedAgentActivitySummary("Updated " + intStringApp(item.Changes) + " file(s)")
	case AgentActivityMcpToolCall:
		return BoundedAgentActivitySummary("MCP " + item.Server + "/" + item.Tool)
	case AgentActivityDynamicToolCall:
		tool := item.Tool
		if item.Namespace != "" {
			tool = item.Namespace + "/" + tool
		}
		return BoundedAgentActivitySummary("Tool " + tool)
	case AgentActivityCollabToolCall:
		switch item.CollabTool {
		case CollabToolSpawnAgent:
			return "Spawned an agent", true
		case CollabToolSendInput:
			return "Sent input to an agent", true
		case CollabToolResumeAgent:
			return "Resumed an agent", true
		case CollabToolWait:
			return "Waited for an agent", true
		case CollabToolCloseAgent:
			return "Closed an agent", true
		default:
			return "", false
		}
	case AgentActivitySubAgentActivity:
		action := ""
		switch item.SubAgentActivity {
		case SubAgentActivityStarted:
			action = "Started"
		case SubAgentActivityInteracted:
			action = "Contacted"
		case SubAgentActivityInterrupted:
			action = "Interrupted"
		default:
			return "", false
		}
		return BoundedAgentActivitySummary(action + " " + item.AgentPath)
	case AgentActivityWebSearch:
		return BoundedAgentActivitySummary("Web search: " + item.Query)
	case AgentActivityImageView:
		return BoundedAgentActivitySummary("Viewed " + item.Path)
	case AgentActivityImageGeneration:
		return "Generated an image", true
	case AgentActivityEnteredReviewMode:
		return "Entered review mode", true
	case AgentActivityExitedReviewMode:
		return "Exited review mode", true
	case AgentActivityContextCompaction:
		return "Compacted context", true
	default:
		return "", false
	}
}

func BoundedAgentActivitySummary(summary string) (string, bool) {
	summary = truncateRunesApp(summary, AgentStatusPreviewGraphemes)
	summary = strings.Join(strings.Fields(summary), " ")
	if summary == "" {
		return "", false
	}
	return summary, true
}

func wrapWords(text string, width int) []string {
	if width <= 0 {
		width = 1
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := []string{}
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
			continue
		}
		if len([]rune(current))+1+len([]rune(word)) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func truncateRunesApp(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes < 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func intStringApp(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
