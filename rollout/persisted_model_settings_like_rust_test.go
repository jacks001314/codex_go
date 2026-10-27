package rollout

import (
	"encoding/json"
	"testing"
)

func modelSettingsTestLine(threadID string, tier string, summary string, personality string) Line {
	settings := map[string]any{
		"model":             "gpt-5.5",
		"model_provider_id": "mock_provider",
		"cwd":               "/repo",
		"service_tier":      tier,
		"reasoning_summary": summary,
		"personality":       personality,
	}
	encoded, _ := json.Marshal(map[string]any{
		"type":            "thread_settings_applied",
		"thread_id":       threadID,
		"thread_settings": settings,
	})
	return Line{Payload: encoded}
}

// Mirrors Rust #55923 latest_persisted_model_settings: the newest thread-owned
// snapshot wins, an ownerless snapshot is accepted only before a foreign
// snapshot is crossed, and TurnContext entries never supply model settings.
func TestLatestPersistedModelSettingsOwnershipLikeRust(t *testing.T) {
	owned := modelSettingsTestLine("thread-1", "flex", "concise", "friendly")
	ownerless := modelSettingsTestLine("", "priority", "detailed", "none")
	foreign := modelSettingsTestLine("thread-2", "default", "auto", "pragmatic")
	turnContext := Line{TurnContext: json.RawMessage(`{"service_tier":"priority","reasoning_summary":"detailed","personality":"none"}`)}

	if got, ok := LatestPersistedModelSettings("thread-1", []Line{turnContext, owned}); !ok ||
		got.ServiceTier == nil || *got.ServiceTier != "flex" ||
		got.ReasoningSummary == nil || *got.ReasoningSummary != "concise" ||
		got.Personality == nil || *got.Personality != "friendly" {
		t.Fatalf("owned settings = %#v, ok=%v", got, ok)
	}
	if got, ok := LatestPersistedModelSettings("thread-1", []Line{owned, ownerless}); !ok ||
		got.ServiceTier == nil || *got.ServiceTier != "priority" {
		t.Fatalf("ownerless settings = %#v, ok=%v", got, ok)
	}
	if got, ok := LatestPersistedModelSettings("thread-1", []Line{owned, ownerless, foreign}); !ok ||
		got.ServiceTier == nil || *got.ServiceTier != "flex" {
		t.Fatalf("crossed foreign settings = %#v, ok=%v", got, ok)
	}
	if _, ok := LatestPersistedModelSettings("thread-1", []Line{ownerless, foreign}); ok {
		t.Fatal("ownerless snapshot after a foreign snapshot must be ignored")
	}
	if _, ok := LatestPersistedModelSettings("thread-1", []Line{turnContext}); ok {
		t.Fatal("TurnContext must not supply model settings")
	}
}
