package telemetry

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Mirrors Rust #44586: the repository URL still contributes to the derived
// skill ID, but skill invocation analytics never serialize it in event_params.
func TestSkillInvocationEventPayloadOmitsRepoURLLikeRust(t *testing.T) {
	raw, err := json.Marshal(SkillInvocationEventRequest{
		EventType: SkillInvocationEventType,
		SkillID:   "skill-sha1",
		SkillName: "doc",
		EventParams: SkillInvocationEventParams{
			ProductClientID: stringPtrTelemetry("codex-cli"),
			SkillScope:      stringPtrTelemetry("repo"),
			PluginID:        stringPtrTelemetry("sample@openai-curated-remote"),
			RemotePluginID:  stringPtrTelemetry("plugins~Plugin_sample"),
			ThreadID:        stringPtrTelemetry("thread-1"),
			TurnID:          stringPtrTelemetry("turn-1"),
			VoiceSessionID:  stringPtrTelemetry("voice-1"),
			InvokeType:      stringPtrTelemetry(SkillInvocationTypeExplicit),
			ModelSlug:       stringPtrTelemetry("gpt-5"),
		},
	})
	if err != nil {
		t.Fatalf("marshal skill invocation event error = %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("unmarshal skill invocation event error = %v", err)
	}
	params, ok := event["event_params"].(map[string]any)
	if !ok {
		t.Fatalf("event_params = %#v, want object", event["event_params"])
	}
	if _, present := params["repo_url"]; present {
		t.Fatalf("event_params contains repo_url: %s", raw)
	}
	wantParams := map[string]any{
		"product_client_id": "codex-cli",
		"skill_scope":       "repo",
		"plugin_id":         "sample@openai-curated-remote",
		"remote_plugin_id":  "plugins~Plugin_sample",
		"thread_id":         "thread-1",
		"turn_id":           "turn-1",
		"voice_session_id":  "voice-1",
		"invoke_type":       SkillInvocationTypeExplicit,
		"model_slug":        "gpt-5",
	}
	if !reflect.DeepEqual(params, wantParams) {
		t.Fatalf("event_params = %#v, want %#v", params, wantParams)
	}
}
