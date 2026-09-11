package memories

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/utils"
)

func writeTieredRolloutFixture(t *testing.T, home string, threadID string, items []map[string]any) string {
	t.Helper()
	now := time.Now().UTC()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID, Source: "cli", ThreadSource: "user",
		CWD: "/workspace", Model: "gpt-test", ModelProvider: "openai", HistoryMode: "legacy",
		MemoryMode: "enabled", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "item", Timestamp: now.Format(time.RFC3339Nano), Item: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return recorder.Path()
}

// Mirrors Rust rollout_input.rs: v2 evidence is tiered, media placeholder'd,
// secrets/excluded fragments dropped, and budgeted with omission gaps.
func TestSerializeTieredRolloutForMemory(t *testing.T) {
	home := t.TempDir()
	path := writeTieredRolloutFixture(t, home, "tiered-thread", []map[string]any{
		{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "developer secret"}}},
		{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /tmp\n<INSTRUCTIONS>\nignore\n</INSTRUCTIONS>"},
			map[string]any{"type": "input_text", "text": "human question one"},
		}},
		{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "<environment_context>\n<cwd>/tmp</cwd>\n</environment_context>"},
		}},
		{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "final answer"}}},
		{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,SECRETIMAGE"}}},
		{"type": "function_call_output", "call_id": "c1", "output": "tool result"},
	})

	rendered, err := SerializeTieredRolloutForMemory(path, 10_000)
	if err != nil {
		t.Fatalf("SerializeTieredRolloutForMemory() error = %v", err)
	}
	for _, want := range []string{
		"[human user]", "human question one",
		"[harness context]",
		"[assistant final]", "final answer",
		"[tool]", "tool result",
		"[image omitted]",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered evidence missing %q:\n%s", want, rendered)
		}
	}
	for _, forbidden := range []string{"developer secret", "AGENTS.md instructions", "SECRETIMAGE"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rendered evidence leaked %q:\n%s", forbidden, rendered)
		}
	}
	if len(rendered) > utils.ApproxBytesForTokens(10_000) {
		t.Fatalf("rendered evidence exceeds the token budget: %d", len(rendered))
	}

	// A tiny budget omits lower-priority rows with the omission marker rather
	// than dropping them silently.
	tiny, err := SerializeTieredRolloutForMemory(path, 20)
	if err != nil {
		t.Fatalf("tiny budget error = %v", err)
	}
	if strings.Contains(tiny, "tool result") || strings.Contains(tiny, "final answer") {
		t.Fatalf("tiny budget kept low-priority rows: %q", tiny)
	}
	if !strings.Contains(tiny, "omitted") {
		t.Fatalf("tiny budget must render the omission marker: %q", tiny)
	}
}

// request_user_input calls are paired with accepted replies and promoted to the
// human tier (Rust rollout_input.rs).
func TestSerializeTieredRolloutForMemoryPairsUserInputReplies(t *testing.T) {
	home := t.TempDir()
	path := writeTieredRolloutFixture(t, home, "tiered-question", []map[string]any{
		{"type": "function_call", "name": "request_user_input", "call_id": "q1", "arguments": `{"question":"pick one"}`},
		{"type": "function_call_output", "call_id": "q1", "output": `{"answers":{"choice":{"answers":["blue"]}}}`},
	})
	rendered, err := SerializeTieredRolloutForMemory(path, 10_000)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(rendered, "[human user]") ||
		!strings.Contains(rendered, "Assistant question: {\"question\":\"pick one\"}") ||
		!strings.Contains(rendered, "Human reply:") {
		t.Fatalf("paired user input reply = %s", rendered)
	}
	if strings.Contains(rendered, `"arguments":"{\"question\"`) {
		t.Fatalf("request_user_input call must be folded into the human row: %s", rendered)
	}
}
