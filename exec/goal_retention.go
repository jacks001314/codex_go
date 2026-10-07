package exec

import (
	"encoding/json"
	"strings"

	"codex_go/compact"
	contextfrag "codex_go/context"
	"codex_go/session"
)

// This file ports the goal half of Rust `Session::replace_compacted_history`
// (codex-rs/core/src/session/mod.rs, #49598 `de02016798`):
//
//	// Goal edits are published outside the running task. Keep edits accepted
//	// after the compaction input snapshot, in their original order, after its
//	// replacement.
//
// Rust implements this once in core, so every entrypoint that compacts (the
// app-server and the CLI/exec path) runs it. Go re-implements the replacement
// install per compaction site, so the exec path applies the same rule here, in
// the same shape as appserver/compact_remote.go's
// retainGoalInstructionsAcrossCompaction.
//
// A shared home in compact/ is not possible: `go list -deps ./session/` shows
// codex_go/session already depends on codex_go/compact, so compact/ importing
// session.Item would create an import cycle.

// execUserGoalInstructionText mirrors Rust `UserGoalUpdate::message_text`
// (`codex-rs/core/src/context/user_goal.rs`, #49598). Only a host-annotated goal
// instruction counts: a user message whose single content item is input text
// and whose `internal_chat_message_metadata_passthrough.content_item_kinds`
// names exactly one of the goal kinds. A matching text wrapper alone is not
// enough, and neither is an item with several content kinds.
func execUserGoalInstructionText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var message struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Metadata struct {
			ContentItemKinds []string `json:"content_item_kinds"`
		} `json:"internal_chat_message_metadata_passthrough"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return "", false
	}
	if message.Type != "message" || message.Role != "user" {
		return "", false
	}
	if len(message.Content) != 1 || message.Content[0].Type != "input_text" {
		return "", false
	}
	if len(message.Metadata.ContentItemKinds) != 1 {
		return "", false
	}
	kind := message.Metadata.ContentItemKinds[0]
	if kind != contextfrag.UserGoalContentKind && kind != contextfrag.UserGoalOmittedObjectiveKind {
		return "", false
	}
	return message.Content[0].Text, true
}

// execUserGoalInstructionIDs mirrors Rust `UserGoalUpdate::message_ids`: the ids
// of the goal instructions a compaction input carries, used as the
// `input_goal_ids` snapshot.
func execUserGoalInstructionIDs(items []compact.Item) map[string]bool {
	ids := map[string]bool{}
	for i := range items {
		if _, ok := execUserGoalInstructionText(items[i].Raw); !ok {
			continue
		}
		if id := strings.TrimSpace(items[i].ID); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// retainExecGoalInstructionsAcrossCompaction keeps goal edits accepted after the
// compaction input snapshot, in their original order, after its replacement. An
// edit published while the compaction ran is therefore neither summarized nor
// part of the replacement, so it is appended unless the compaction input
// (inputGoalIDs) or the replacement itself already covers it.
func retainExecGoalInstructionsAcrossCompaction(replacement []session.Item, live []session.Item, inputGoalIDs map[string]bool) []session.Item {
	if len(live) == 0 {
		return replacement
	}
	replacementIDs := map[string]bool{}
	for i := range replacement {
		if _, ok := execUserGoalInstructionText(replacement[i].Raw); !ok {
			continue
		}
		if id := strings.TrimSpace(replacement[i].ID); id != "" {
			replacementIDs[id] = true
		}
	}
	var carried []session.Item
	for i := range live {
		item := live[i]
		if _, ok := execUserGoalInstructionText(item.Raw); !ok {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" || inputGoalIDs[id] || replacementIDs[id] {
			continue
		}
		carried = append(carried, item)
	}
	if len(carried) == 0 {
		return replacement
	}
	out := make([]session.Item, 0, len(replacement)+len(carried))
	out = append(out, replacement...)
	out = append(out, carried...)
	return out
}
