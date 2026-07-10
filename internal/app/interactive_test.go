package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/internal/session"
)

func TestInteractiveSessionMessagesHideContextInstructionItems(t *testing.T) {
	record := &session.Record{Items: []session.Item{{
		ID:        "skill-instructions-turn-1-1",
		Type:      "message",
		Role:      "developer",
		Text:      "<skill>\n<name>imagegen</name>\nHidden instructions.\n</skill>",
		CreatedAt: time.Unix(1700000000, 0).UTC(),
		Data:      map[string]any{"kind": "skill_instructions"},
	}, {
		ID:        "image-generation-instructions-ig_123",
		Type:      "message",
		Role:      "developer",
		Text:      "Generated images are saved to C:/tmp as C:/tmp/<image_id>.png by default.",
		CreatedAt: time.Unix(1700000001, 0).UTC(),
		Metadata:  map[string]any{"kind": "image_generation_instructions"},
	}, {
		ID:        "user-1",
		Type:      "message",
		Role:      "user",
		Text:      "visible",
		CreatedAt: time.Unix(1700000002, 0).UTC(),
	}}}

	messages := interactiveSessionMessagesFromRecord(record)
	if len(messages) != 1 || messages[0].Text != "visible" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestInteractiveRepairImageGenerationItemsSavesLegacyRawCall(t *testing.T) {
	home := t.TempDir()
	record := &session.Record{
		ID: "thread-legacy",
		Items: []session.Item{{
			ID:        "ig_legacy",
			Type:      "image_generation_call",
			Role:      "assistant",
			Text:      "Zm9v",
			CreatedAt: time.Unix(1700000000, 0).UTC(),
			Data: map[string]any{
				"result":         "Zm9v",
				"revised_prompt": "legacy prompt",
			},
		}},
	}

	if !interactiveRepairImageGenerationItems(record, home) {
		t.Fatalf("expected repair to change record")
	}
	item := record.Items[0]
	if item.Type != "imageGeneration" || item.Role != "" || item.Text != "legacy prompt" {
		t.Fatalf("repaired item = %#v", item)
	}
	savedPath := interactiveSessionItemDataString(item, "savedPath", "saved_path")
	if savedPath == "" || filepath.Base(savedPath) != "ig_legacy.png" {
		t.Fatalf("saved path = %q", savedPath)
	}
	bytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", savedPath, err)
	}
	if string(bytes) != "foo" {
		t.Fatalf("saved bytes = %q", string(bytes))
	}
	message, ok := interactiveSessionMessageFromItem(item)
	if !ok || strings.Contains(message.Text, "Zm9v") || !strings.Contains(message.Text, "Saved to: "+savedPath) {
		t.Fatalf("message = %#v ok=%v", message, ok)
	}
}
