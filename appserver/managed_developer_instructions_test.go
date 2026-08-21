package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/config"
)

func TestManagedDeveloperInstructionsInputItemMirrorsRust(t *testing.T) {
	// Rust #39755: requirements-owned instructions are contributed as a
	// separate developer message with the managed markers and rejected when
	// they exceed the 10,000-token bound.
	router := NewRuntimeRouter(RuntimeServices{})
	instructions := "Follow the company policy."
	cfg := &config.Config{Requirements: &config.ConfigRequirements{AdditionalDeveloperInstructions: &instructions}}
	item, err := router.managedDeveloperInstructionsInputItem(cfg)
	if err != nil {
		t.Fatalf("managedDeveloperInstructionsInputItem() error = %v", err)
	}
	if item == nil {
		t.Fatal("managed developer instructions item = nil, want developer message")
	}
	raw, err := marshalAppserverTestInputItem(item)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	text := strings.Join(inputItemTexts(decoded), " ")
	if !strings.Contains(text, "<managed_developer_instructions>") || !strings.Contains(text, "Follow the company policy.") {
		t.Fatalf("managed developer item missing markers/body: %s", raw)
	}

	absent := &config.Config{Requirements: &config.ConfigRequirements{}}
	if item, err := router.managedDeveloperInstructionsInputItem(absent); err != nil || item != nil {
		t.Fatalf("absent item = %v err=%v, want nil", item, err)
	}

	oversized := strings.Repeat("x", 60*1024)
	cfg.Requirements.AdditionalDeveloperInstructions = &oversized
	if _, err := router.managedDeveloperInstructionsInputItem(cfg); err == nil || !strings.Contains(err.Error(), "10000-token limit") {
		t.Fatalf("oversized error = %v, want 10,000-token limit", err)
	}
}

func inputItemTexts(item map[string]any) []string {
	var out []string
	content, _ := item["content"].([]any)
	for _, part := range content {
		if typed, ok := part.(map[string]any); ok {
			if text, ok := typed["text"].(string); ok && text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func marshalAppserverTestInputItem(item any) ([]byte, error) {
	switch value := item.(type) {
	case map[string]any:
		return json.Marshal(value)
	case interface{ MarshalJSON() ([]byte, error) }:
		return value.MarshalJSON()
	default:
		return json.Marshal(item)
	}
}
