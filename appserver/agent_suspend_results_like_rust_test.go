package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #61198 `save_evicted_parent_results`: a terminal child result
// that is still queued when the tree is suspended is written to the parent's
// history and rollout, so a replacement host replays it from the recorded
// conversation instead of losing this process's mailbox. Non-terminal mail
// stays queued exactly as before.
func TestSuspendAgentTreePersistsQueuedTerminalResultsLikeRust(t *testing.T) {
	router, threadID := newAgentCommunicationRolloutRouter(t)
	controller := newRuntimeAgentController(router, string(threadID), t.TempDir(), 1).(*runtimeAgentController)
	progress := agentCommunicationInputItem("/root/worker", "/root", "Message Type: MESSAGE\nTask name: /root\nSender: /root/worker\nPayload:\nstill working")
	terminal := agentCommunicationInputItem("/root/worker", "/root", "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/worker\nPayload:\ndone")
	router.enqueueRuntimeAgentMessage(string(threadID), progress)
	router.enqueueRuntimeAgentMessage(string(threadID), terminal)

	if _, err := controller.SuspendAgentTree(context.Background(), string(threadID)); err != nil {
		t.Fatalf("SuspendAgentTree() error = %v", err)
	}

	queued := router.drainRuntimeAgentMessages(string(threadID))
	if len(queued) != 1 {
		t.Fatalf("queued mail after suspension = %#v, want only the progress update", queued)
	}
	if payload, ok := configurationUpdatePayload(queued[0]); !ok || !jsonContainsText(payload, "still working") {
		t.Fatalf("queued mail = %#v, want the non-terminal progress update", queued[0])
	}

	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %v, %v", record, err)
	}
	recorded := 0
	for index := range record.Items {
		item := &record.Items[index]
		if item.Type != "agent_message" || !jsonContainsText(item.Raw, "Message Type: FINAL_ANSWER") {
			continue
		}
		recorded++
	}
	if recorded != 1 {
		t.Fatalf("recorded terminal results = %d, want the suspended result in the parent history", recorded)
	}

	lines := loadAgentCommunicationRolloutLines(t, router, threadID)
	boundaries := 0
	items := 0
	for index := range lines {
		switch lines[index].Type {
		case "inter_agent_communication_metadata":
			boundaries++
		case "item":
			items++
			var payload struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(lines[index].Item, &payload); err != nil {
				t.Fatalf("item payload = %s: %v", lines[index].Item, err)
			}
			if payload.Type != "agent_message" || !jsonContainsText(lines[index].Item, "Message Type: FINAL_ANSWER") {
				t.Fatalf("rollout item = %s, want the suspended terminal result", lines[index].Item)
			}
		}
	}
	if boundaries != 1 || items != 1 {
		t.Fatalf("rollout boundaries = %d items = %d, want one recorded result with its boundary", boundaries, items)
	}

	// A second suspension observes the stopped tree and records nothing new.
	if _, err := controller.SuspendAgentTree(context.Background(), string(threadID)); err != nil {
		t.Fatalf("second SuspendAgentTree() error = %v", err)
	}
	again := loadAgentCommunicationRolloutLines(t, router, threadID)
	if len(again) != len(lines) {
		t.Fatalf("repeated suspension appended %d rollout lines", len(again)-len(lines))
	}
}

func jsonContainsText(value any, text string) bool {
	switch typed := value.(type) {
	case json.RawMessage:
		return strings.Contains(string(typed), text)
	case []byte:
		return strings.Contains(string(typed), text)
	case string:
		return strings.Contains(typed, text)
	case map[string]any:
		for _, entry := range typed {
			if jsonContainsText(entry, text) {
				return true
			}
		}
	case []any:
		for _, entry := range typed {
			if jsonContainsText(entry, text) {
				return true
			}
		}
	}
	return false
}
