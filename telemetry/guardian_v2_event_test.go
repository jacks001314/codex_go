package telemetry

import (
	"encoding/json"
	"testing"
)

func TestGuardianV2EventRequestsSerializeLikeRust(t *testing.T) {
	risk := "low"
	event := NewGuardianV2ClassificationEvent(GuardianV2EventParams{
		SessionID:       "session-1",
		AppServerClient: CodexAppServerClientMetadata{ProductClientID: "vscode"},
		Runtime:         CurrentRuntimeMetadata(),
		ThreadSource:    stringPointer("user"),
		SubagentSource:  stringPointer("root"),
		ParentThreadID:  stringPointer("parent"),
		GuardianV2Event: GuardianV2Event{
			ThreadID:     "thread-1",
			TurnID:       "turn-1",
			ItemID:       stringPointer("item-1"),
			Model:        stringPointer("gpt-guardian"),
			OccurredAtMS: 123,
			Outcome:      "allow",
			RiskLevel:    &risk,
			DurationMS:   7,
		},
	})
	if event.EventType != CodexGuardianV2ClassificationEventType {
		t.Fatalf("EventType = %q", event.EventType)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{`"outcome":"allow"`, `"risk_level":"low"`, `"thread_id":"thread-1"`} {
		if !jsonContainsString(data, want) {
			t.Fatalf("Marshal() = %s missing %q", data, want)
		}
	}

	fast := NewGuardianV2FastDecisionEvent(GuardianV2EventParams{
		SessionID: "session-2",
		GuardianV2Event: GuardianV2Event{
			ThreadID:     "thread-2",
			TurnID:       "turn-2",
			OccurredAtMS: 9,
			Decision:     "approved",
		},
	})
	data, err = json.Marshal(fast)
	if err != nil {
		t.Fatalf("fast Marshal() error = %v", err)
	}
	if !jsonContainsString(data, `"decision":"approved"`) {
		t.Fatalf("fast Marshal() = %s", data)
	}
}

func stringPointer(value string) *string { return &value }

func jsonContainsString(data []byte, fragment string) bool {
	return len(data) > 0 && stringContains(string(data), fragment)
}

func stringContains(text, fragment string) bool {
	for i := 0; i+len(fragment) <= len(text); i++ {
		if text[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
