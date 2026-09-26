package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/eventmap"
	"codex_go/session"
)

// Rust parity: codex-rs/core/src/session/mod.rs's cumulative-attribution
// checkpoint block. Every recorded response-item batch writes the thread's MCP
// attribution checkpoint into the first item that durable rollout storage keeps,
// repeats it at each user-turn boundary so a last-N-turn fork keeps a
// self-contained suffix, and acknowledges the revision once the batch is
// persisted.

// shouldPersistResponseItem mirrors codex-rollout's `should_persist_response_item`:
// only these response-item kinds reach durable rollout storage, so they are the
// only carriers of the MCP attribution checkpoint.
func shouldPersistResponseItem(item session.Item) bool {
	switch item.Type {
	case "message", "agent_message", "reasoning", "local_shell_call", "function_call",
		"tool_search_call", "function_call_output", "tool_search_output",
		"custom_tool_call", "custom_tool_call_output", "web_search_call",
		"image_generation_call", "configuration_update", "compaction", "context_compaction":
		return true
	default:
		// additional_tools, compaction_trigger and other kinds are not persisted.
		return false
	}
}

// isMcpAttributionTurnBoundary mirrors `context_manager::is_user_turn_boundary`:
// an agent message, a user message that is not contextual evidence, or an
// assistant message carrying an inter-agent instruction marks a turn boundary
// where the checkpoint must be repeated.
func isMcpAttributionTurnBoundary(item session.Item) bool {
	switch item.Type {
	case "agent_message":
		return true
	case "message":
		content := sessionItemContentItems(item)
		switch item.Role {
		case "user":
			return !eventmap.IsContextualUserMessageContent(content)
		case "assistant":
			return isInterAgentInstructionContent(content)
		}
	}
	return false
}

// sessionItemContentItems renders a session item's content for the contextual
// and inter-agent classification, falling back to the plain text field.
func sessionItemContentItems(item session.Item) []eventmap.ContentItem {
	if len(item.Content) > 0 {
		out := make([]eventmap.ContentItem, 0, len(item.Content))
		for _, part := range item.Content {
			out = append(out, eventmap.ContentItem{Kind: eventmap.ContentKind(part.Type), Text: part.Text})
		}
		return out
	}
	if item.Text != "" {
		return []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: item.Text}}
	}
	return nil
}

// isInterAgentInstructionContent mirrors
// `InterAgentCommunication::is_message_content`: the content is a single text
// item whose text parses as an inter-agent communication. Go does not model the
// protocol struct yet, so the required members are checked directly instead of
// deserializing it.
func isInterAgentInstructionContent(content []eventmap.ContentItem) bool {
	if len(content) != 1 {
		return false
	}
	switch content[0].Kind {
	case eventmap.ContentInputText, eventmap.ContentOutputText:
	default:
		return false
	}
	return isInterAgentCommunicationJSON(content[0].Text)
}

// isInterAgentCommunicationJSON reports whether the text decodes as Rust's
// `InterAgentCommunication`: `author`, `recipient`, `content` and `trigger_turn`
// are required, and unknown members are ignored exactly like serde without
// `deny_unknown_fields`.
func isInterAgentCommunicationJSON(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		return false
	}
	for _, required := range []string{"author", "recipient", "content", "trigger_turn"} {
		if _, ok := fields[required]; !ok {
			return false
		}
	}
	var content string
	if err := json.Unmarshal(fields["content"], &content); err != nil {
		return false
	}
	var triggerTurn bool
	if err := json.Unmarshal(fields["trigger_turn"], &triggerTurn); err != nil {
		return false
	}
	return true
}

// annotateMcpAttributionMetadata writes the thread's pending MCP attribution
// checkpoint into the items about to be appended and returns the revision to
// acknowledge once the batch is persisted. `force` writes an unchanged
// checkpoint at a turn boundary, matching Rust's `force_mcp_checkpoint`, so a
// fork's retained suffix stays self-contained.
func (r *RuntimeRouter) annotateMcpAttributionMetadata(threadID string, items []session.Item) (uint64, bool) {
	if r == nil || len(items) == 0 {
		return 0, false
	}
	recorder := r.executedToolCallRecorder(threadID)
	if recorder == nil {
		return 0, false
	}
	forced := false
	for i := range items {
		if isMcpAttributionTurnBoundary(items[i]) {
			forced = true
			break
		}
	}
	attribution, revision, ok := recorder.McpAttributionCheckpoint(forced)
	if !ok {
		return 0, false
	}
	firstPersisted := -1
	for i := range items {
		if shouldPersistResponseItem(items[i]) {
			firstPersisted = i
			break
		}
	}
	if firstPersisted < 0 {
		// The batch has no durable carrier; Rust leaves the checkpoint unacknowledged
		// so the next persisted batch writes it.
		return 0, false
	}
	for i := range items {
		if i == firstPersisted || isMcpAttributionTurnBoundary(items[i]) {
			mergeSessionItemHarnessMetadata(&items[i], map[string]any{"mcp_attribution": attribution})
		}
	}
	return revision, true
}

// markMcpAttributionPersisted acknowledges the checkpoint revision once the
// batch reached durable storage.
func (r *RuntimeRouter) markMcpAttributionPersisted(threadID string, revision uint64) {
	if r == nil {
		return
	}
	if recorder := r.executedToolCallRecorder(threadID); recorder != nil {
		recorder.MarkMcpAttributionPersisted(revision)
	}
}
