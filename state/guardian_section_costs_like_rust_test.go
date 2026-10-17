package state

import "testing"

// Mirrors Rust #44181 SectionCost::measurements and logical transcript
// aggregation without exposing prompt payloads in telemetry.
func TestGuardianPromptSectionCostsLikeRust(t *testing.T) {
	parts, costs, err := BuildPromptPartsWithSectionCosts(
		Action{Type: "command", Command: "echo hello", CWD: "/workspace"},
		[]GuardianTranscriptEntry{
			{Text: "user: inspect the workspace"},
			{Text: "assistant: inspected", Native: []byte(`{"type":"agent_message","content":"ok"}`)},
			{Text: "user: continue"},
		},
		BuildPromptOptions{
			PermissionContext: &PermissionContext{DeniedPaths: []string{"/secret"}},
		},
	)
	if err != nil {
		t.Fatalf("BuildPromptPartsWithSectionCosts() error = %v", err)
	}
	if len(parts) == 0 {
		t.Fatal("prompt parts = nil")
	}
	var transcript *GuardianPromptSectionCost
	var permissions *GuardianPromptSectionCost
	var plannedAction *GuardianPromptSectionCost
	for index := range costs {
		switch costs[index].Section {
		case "conversation_transcript":
			transcript = &costs[index]
		case "permissions":
			permissions = &costs[index]
		case "planned_action":
			plannedAction = &costs[index]
		}
	}
	if transcript == nil || permissions == nil || plannedAction == nil {
		t.Fatalf("section costs = %#v", costs)
	}
	if transcript.TextBytes == 0 {
		t.Fatalf("transcript cost = %#v, want text and native message bytes", transcript)
	}
	for _, cost := range []GuardianPromptSectionCost{*transcript, *permissions, *plannedAction} {
		measurements := cost.Measurements()
		if len(measurements) != 4 ||
			measurements[0].Measurement != "text_bytes" ||
			measurements[1].Measurement != "estimated_text_tokens" ||
			measurements[2].Measurement != "image_bytes" ||
			measurements[3].Measurement != "image_count" {
			t.Fatalf("measurements for %s = %#v", cost.Section, measurements)
		}
		if measurements[1].Value != approximateTokensFromBytes(cost.TextBytes) {
			t.Fatalf("estimated tokens for %s = %d, bytes = %d", cost.Section, measurements[1].Value, cost.TextBytes)
		}
	}
}
