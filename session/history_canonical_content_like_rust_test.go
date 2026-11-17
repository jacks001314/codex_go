package session

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust replays conversation history from typed ResponseItems, so every message
// item reaching the model already carries Responses content types
// (input_text/output_text/input_image/input_file) and a role. Go's rollout also
// stores the TUI-facing TurnItem projection, whose content uses the display
// vocabulary (Text/text/image/text_elements). Replaying those shapes verbatim
// fails provider validation, so the history builder must normalize them no
// matter which shape the record kept.
func TestInputItemsFromRecordNormalizesCanonicalTurnItemContentLikeRust(t *testing.T) {
	agentRaw := json.RawMessage(`{"type":"AgentMessage","id":"msg_a","content":[{"type":"Text","text":"done"}]}`)
	userRaw := json.RawMessage(`{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"look","text_elements":[]},{"type":"image","detail":"high","image_url":"data:image/png;base64,AAAA"},{"type":"file","file_id":"file-1"}]}`)
	rolelessRaw := json.RawMessage(`{"type":"message","content":[{"type":"text","text":"no role"}]}`)
	record := &Record{Items: []Item{
		{ID: "u1", Type: "message", Role: "user", Raw: userRaw},
		{ID: "msg_a", Type: "agent_message", Role: "assistant", Raw: agentRaw},
		{ID: "m2", Type: "message", Raw: rolelessRaw},
		// Rollout replay of an `event_msg item_completed` AgentMessage keeps the
		// display content parts (`Text`), which must still normalize.
		{ID: "msg_b", Type: "agent_message", Role: "assistant", Content: []ContentPart{{Type: "Text", Text: "replayed"}}},
	}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 4 {
		t.Fatalf("items = %#v, want 4", items)
	}
	allowed := map[string]bool{
		"input_text": true, "output_text": true, "input_image": true,
		"input_file": true, "input_audio": true,
	}
	for index, value := range items {
		message, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("item %d = %#v", index, value)
		}
		if message["type"] != "message" {
			t.Fatalf("item %d type = %#v, want message", index, message["type"])
		}
		if role, _ := message["role"].(string); strings.TrimSpace(role) == "" {
			t.Fatalf("item %d has no role: %#v", index, message)
		}
		blocks := messageContentBlocks(message["content"])
		if len(blocks) == 0 {
			t.Fatalf("item %d has no content: %#v", index, message)
		}
		for _, block := range blocks {
			if !allowed[block["type"].(string)] {
				t.Fatalf("item %d has non-Responses content type %#v", index, block["type"])
			}
			if block["type"] == "input_image" {
				if detail, _ := block["detail"].(string); strings.TrimSpace(detail) == "" {
					t.Fatalf("input_image without detail: %#v", block)
				}
			}
			if _, present := block["text_elements"]; present {
				t.Fatalf("text_elements leaked: %#v", block)
			}
		}
	}
	user := items[0].(map[string]any)
	blocks := messageContentBlocks(user["content"])
	if blocks[0]["type"] != "input_text" || blocks[0]["text"] != "look" {
		t.Fatalf("user text block = %#v", blocks[0])
	}
	if blocks[1]["type"] != "input_image" || blocks[1]["image_url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("user image block = %#v", blocks[1])
	}
	if blocks[2]["type"] != "input_file" || blocks[2]["file_id"] != "file-1" {
		t.Fatalf("user file block = %#v", blocks[2])
	}
	assistant := items[1].(map[string]any)
	assistantBlocks := messageContentBlocks(assistant["content"])
	if assistantBlocks[0]["type"] != "output_text" || assistantBlocks[0]["text"] != "done" {
		t.Fatalf("assistant block = %#v", assistantBlocks[0])
	}
}

// messageContentBlocks coerces a replayed message's content, which stays
// []map[string]any when it was rebuilt from a record and []any when it was
// unmarshaled from a recorded payload.
func messageContentBlocks(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		blocks := make([]map[string]any, 0, len(typed))
		for _, entry := range typed {
			if block, ok := entry.(map[string]any); ok {
				blocks = append(blocks, block)
			}
		}
		return blocks
	default:
		return nil
	}
}
