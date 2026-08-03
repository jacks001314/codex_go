package historycell

import (
	"sort"
	"strings"

	"codex_go/protocol"
)

func NewCollabAgentToolCall(item *protocol.ThreadItem, completed bool) (PlainHistoryCell, bool) {
	if item == nil {
		return PlainHistoryCell{}, false
	}
	tool := strings.TrimSpace(item.Tool)
	if tool == "" {
		tool = metadataString(item.Metadata, "tool")
	}
	receivers := []string{}
	if item.ReceiverThreadIDs != nil {
		receivers = append(receivers, (*item.ReceiverThreadIDs)...)
	}
	if len(receivers) == 0 {
		receivers = metadataStringSlice(item.Metadata, "receiverThreadIds", "receiver_thread_ids")
	}
	prompt := ""
	if item.Prompt != nil && !looksEncryptedCollabPrompt(*item.Prompt) {
		prompt = strings.TrimSpace(*item.Prompt)
	}
	label := "agent"
	if len(receivers) > 0 && strings.TrimSpace(receivers[0]) != "" {
		label = strings.TrimSpace(receivers[0])
	}
	line := ""
	switch tool {
	case "spawnAgent", "spawn_agent":
		if !completed {
			return PlainHistoryCell{}, false
		}
		line = "• Spawned " + label
	case "sendInput", "send_input":
		if !completed {
			return PlainHistoryCell{}, false
		}
		line = "• Sent input to " + label
	case "resumeAgent", "resume_agent":
		if completed {
			line = "• Resumed " + label
		} else {
			line = "• Resuming " + label
		}
	case "wait", "wait_agent":
		if completed {
			line = "• Finished waiting"
		} else if len(receivers) == 1 {
			line = "• Waiting for " + label
		} else if len(receivers) > 1 {
			line = "• Waiting for " + itoa(len(receivers)) + " agents"
		} else {
			line = "• Waiting for agents"
		}
	case "closeAgent", "close_agent":
		if !completed {
			return PlainHistoryCell{}, false
		}
		line = "• Closed " + label
	default:
		return PlainHistoryCell{}, false
	}
	lines := []string{line}
	if prompt != "" && completed && (tool == "spawnAgent" || tool == "spawn_agent" || tool == "sendInput" || tool == "send_input") {
		lines = append(lines, "  └ "+prompt)
	}
	if completed && (tool == "wait" || tool == "wait_agent") {
		states := map[string]protocol.CollabAgentState{}
		if item.AgentsStates != nil {
			for key, state := range *item.AgentsStates {
				states[key] = state
			}
		}
		keys := make([]string, 0, len(states))
		for key := range states {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			lines = append(lines, "  └ No agents completed yet")
		}
		for _, key := range keys {
			state := states[key]
			detail := strings.TrimSpace(state.Status)
			if state.Message != nil && strings.TrimSpace(*state.Message) != "" && !looksEncryptedCollabPrompt(*state.Message) {
				detail += " - " + strings.TrimSpace(*state.Message)
			}
			lines = append(lines, "  └ "+key+": "+detail)
		}
	}
	return NewPlainHistoryCell(lines), true
}

func NewSubAgentActivity(kind string, agentPath string) (PlainHistoryCell, bool) {
	agentPath = strings.TrimSpace(agentPath)
	if agentPath == "" {
		agentPath = "agent"
	}
	var line string
	switch strings.TrimSpace(kind) {
	case "started":
		line = "• Started `" + agentPath + "`"
	case "interacted":
		line = "• Interacted with `" + agentPath + "`"
	case "interrupted":
		line = "• Interrupted `" + agentPath + "`"
	default:
		return PlainHistoryCell{}, false
	}
	return NewPlainHistoryCell([]string{line}), true
}

func looksEncryptedCollabPrompt(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "gAAAA")
}

func metadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := metadata[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func metadataStringSlice(metadata map[string]any, keys ...string) []string {
	for _, key := range keys {
		switch values := metadata[key].(type) {
		case []string:
			return append([]string(nil), values...)
		case []any:
			out := make([]string, 0, len(values))
			for _, value := range values {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, strings.TrimSpace(text))
				}
			}
			return out
		}
	}
	return nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
