package rollout

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

const moderationLegacySessionMeta = `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}`

func migratedCompletedItems(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var items []map[string]any
	for _, line := range splitCanonicalLines(string(data)) {
		var record struct {
			Payload struct {
				Type string         `json:"type"`
				Item map[string]any `json:"item"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record.Payload.Type == "item_completed" {
			items = append(items, record.Payload.Item)
		}
	}
	return items
}

func splitCanonicalLines(data string) []string {
	var lines []string
	start := 0
	for index := 0; index < len(data); index++ {
		if data[index] == '\n' {
			lines = append(lines, data[start:index])
			start = index + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// Mirrors Rust #60172: a legacy agent_message event's moderation decision
// migrates into the answer item with its display instruction, and the
// decision's item id names the migrated answer.
func TestCanonicalizeLegacyAgentMessageModerationLikeRust(t *testing.T) {
	source := moderationLegacySessionMeta + "\n" + `{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"agent_message","message":"hello","moderation":{"moderated":true,"display":{"mode":"show","version":1,"trustedContacts":"skip","annotation":{"kind":"wellbeingHelpline","subject":"answer"}},"item_id":"msg-mod-1"}}}` + "\n"
	home, path := writeCanonicalMigrationRawRollout(t, source)
	if _, err := CanonicalizeRolloutWithStats(home, path); err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	items := migratedCompletedItems(t, path)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	if item["type"] != "AgentMessage" || item["id"] != "msg-mod-1" {
		t.Fatalf("item header = %#v", item)
	}
	want := map[string]any{
		"moderated": true,
		"display": map[string]any{
			"mode":            "show",
			"version":         float64(1),
			"trustedContacts": "skip",
			"annotation":      map[string]any{"kind": "wellbeingHelpline", "subject": "answer"},
		},
	}
	if !reflect.DeepEqual(item["moderation"], want) {
		t.Fatalf("moderation = %#v, want %#v", item["moderation"], want)
	}
}

// Mirrors Rust #60172: a decision without a display instruction stores only the
// verdict, and the answer keeps its synthesized item id.
func TestCanonicalizeLegacyAgentMessageModerationWithoutDisplayLikeRust(t *testing.T) {
	source := moderationLegacySessionMeta + "\n" + `{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"agent_message","message":"hello","phase":"final_answer","moderation":{"moderated":false}}}` + "\n"
	home, path := writeCanonicalMigrationRawRollout(t, source)
	if _, err := CanonicalizeRolloutWithStats(home, path); err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	items := migratedCompletedItems(t, path)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	moderation, ok := item["moderation"].(map[string]any)
	if !ok || len(moderation) != 1 || moderation["moderated"] != false {
		t.Fatalf("moderation = %#v, want only the stored verdict", item["moderation"])
	}
	if item["phase"] != "final_answer" {
		t.Fatalf("phase = %#v", item["phase"])
	}
	if id, _ := item["id"].(string); id == "" || id == "msg-mod-1" {
		t.Fatalf("item id = %q, want a synthesized id", id)
	}
}
