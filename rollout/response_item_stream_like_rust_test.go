package rollout

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestPaginatedRolloutWritesRustResponseItemStream mirrors Rust's rollout
// writer: `record_conversation_items` persists every model-visible item as a
// `RolloutItem::ResponseItem` line, while TurnItems are separate display-only
// `EventMsg` lines. Go's paginated writer must emit both, otherwise a Rust
// session resumed from a Go rollout has no conversation to reconstruct.
func TestPaginatedRolloutWritesRustResponseItemStream(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-response-stream", SessionID: "thread-response-stream",
		HistoryMode: "paginated", CWD: home, ModelProvider: "openai", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	items := rustResponseStreamItems(now)
	if err := AppendSessionItems(recorder, items, now); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	responseTypes := responseItemTypesFromRollout(t, path)
	want := []string{"message/user", "reasoning/", "message/assistant", "function_call/", "function_call_output/"}
	if strings.Join(responseTypes, ",") != strings.Join(want, ",") {
		t.Fatalf("response_item stream = %v, want %v", responseTypes, want)
	}
	if completed := completedItemCountFromRollout(t, path); completed != len(items) {
		t.Fatalf("item_completed events = %d, want %d", completed, len(items))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"display only", `"type":"text"`} {
		for _, row := range strings.Split(string(data), "\n") {
			if !strings.Contains(row, `"type":"response_item"`) {
				continue
			}
			if strings.Contains(row, legacy) {
				t.Fatalf("response_item row leaked %q: %s", legacy, row)
			}
		}
	}
}

// TestReplayUsesResponseItemsInsteadOfTurnProjections verifies the read half of
// the same contract: a rollout written by Go replays the recorded response items,
// not the TurnItem projections that share their ids or turn.
func TestReplayUsesResponseItemsInsteadOfTurnProjections(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-response-replay", SessionID: "thread-response-replay",
		HistoryMode: "paginated", CWD: home, ModelProvider: "openai", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	if err := AppendSessionItems(recorder, rustResponseStreamItems(now), now); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	replayed := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true})
	if len(replayed) != 5 {
		t.Fatalf("replayed history has %d items, want 5: %#v", len(replayed), replayed)
	}
	if !strings.Contains(marshalJSON(t, replayed[0]), `"input_text"`) {
		t.Fatalf("user message did not replay as input_text: %s", marshalJSON(t, replayed[0]))
	}
	if !strings.Contains(marshalJSON(t, replayed[1]), `"type":"reasoning"`) {
		t.Fatalf("reasoning did not replay from its response item: %s", marshalJSON(t, replayed[1]))
	}
	if !strings.Contains(marshalJSON(t, replayed[2]), `"output_text"`) {
		t.Fatalf("assistant message did not replay as output_text: %s", marshalJSON(t, replayed[2]))
	}

	var recordItemsWithAgentID int
	for i := range record.Items {
		item := &record.Items[i]
		if item.ID != "msg_1" {
			continue
		}
		recordItemsWithAgentID++
		if !session.ModelVisibleHistoryItem(item) {
			t.Fatalf("agent message lost its recorded response item to the TurnItem projection: %#v", item)
		}
	}
	if recordItemsWithAgentID != 1 {
		t.Fatalf("agent message appears %d times in the replayed record, want 1", recordItemsWithAgentID)
	}
}

// TestResponseItemLineKeepsHarnessMetadataLikeRust covers the envelope half of
// Rust's `RolloutLine`: `metadata` carries the harness sidecar
// (`CodexHarnessMetadata`) beside the model payload. It must survive the write,
// stay out of the payload, and remain readable after resume.
func TestResponseItemLineKeepsHarnessMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-harness-metadata", SessionID: "thread-harness-metadata",
		HistoryMode: "paginated", CWD: home, ModelProvider: "openai", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	item := session.Item{
		ID: "call-1", Type: "tool_output", CallID: "call-1", Text: "command output", CreatedAt: now,
		Data: map[string]any{
			"call_id": "call-1",
			"harness_metadata": map[string]any{
				"fallback_token_limit_override": 4096,
				"producer_bounded_tool_audio":   true,
			},
		},
		Metadata: map[string]any{"turnId": "turn-1"},
	}
	if err := AppendSessionItems(recorder, []session.Item{item}, now); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	lines, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for i := range lines {
		if lines[i].Type != "item" || len(lines[i].Item) == 0 {
			continue
		}
		found = true
		if strings.Contains(string(lines[i].Item), "harness_metadata") {
			t.Fatalf("harness metadata leaked into the model payload: %s", lines[i].Item)
		}
		var metadata map[string]any
		if err := json.Unmarshal(lines[i].ItemMetadata, &metadata); err != nil {
			t.Fatalf("response item metadata = %s: %v", lines[i].ItemMetadata, err)
		}
		if metadata["fallback_token_limit_override"] != float64(4096) {
			t.Fatalf("response item metadata = %#v", metadata)
		}
		if _, ok := metadata["producer_bounded_tool_audio"]; ok {
			t.Fatalf("transient tool-audio field was persisted: %#v", metadata)
		}
	}
	if !found {
		t.Fatal("rollout has no response_item line")
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	var resumed *session.Item
	for i := range record.Items {
		if record.Items[i].CallID == "call-1" && record.Items[i].Type != "commandExecution" {
			resumed = &record.Items[i]
		}
	}
	if resumed == nil {
		t.Fatalf("resumed record has no tool output: %#v", record.Items)
	}
	if got := session.ItemHarnessMetadata(resumed)["fallback_token_limit_override"]; got != float64(4096) {
		t.Fatalf("resumed harness metadata = %#v", session.ItemHarnessMetadata(resumed))
	}
}

func rustResponseStreamItems(now time.Time) []session.Item {
	return []session.Item{
		{
			ID: "u1", Type: "message", Role: "user", Text: "run the tool",
			Content:   []session.ContentPart{{Type: "input_text", Text: "run the tool"}},
			CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"},
		},
		{
			ID: "rs_1", Type: "reasoning", Text: "checking the date", CreatedAt: now,
			Raw:      json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"checking the date"}],"encrypted_content":"sig"}`),
			Metadata: map[string]any{"turnId": "turn-1"},
		},
		{
			ID: "msg_1", Type: "agent_message", Role: "assistant", Text: "running it", CreatedAt: now,
			Raw:      json.RawMessage(`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"running it"}]}`),
			Metadata: map[string]any{"turnId": "turn-1"},
		},
		{
			ID: "call-1", Type: "function_call", Name: "shell", CallID: "call-1", Text: `{"cmd":"date"}`,
			Data:      map[string]any{"arguments": `{"cmd":"date"}`, "command": "date"},
			CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"},
		},
		{
			ID: "out-1", Type: "tool_output", CallID: "call-1", Text: "Mon Nov 16", CreatedAt: now,
			Metadata: map[string]any{"turnId": "turn-1"},
		},
		{
			ID: "cmd-1", Type: "commandExecution", CallID: "call-1", Text: "display only", CreatedAt: now,
			Data:     map[string]any{"aggregatedOutput": "display only"},
			Metadata: map[string]any{"turnId": "turn-1"},
		},
	}
}

func responseItemTypesFromRollout(t *testing.T, path string) []string {
	t.Helper()
	lines, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for i := range lines {
		// Load folds `response_item` rows into the legacy `item` shape.
		if lines[i].Type != "item" || len(lines[i].Item) == 0 {
			continue
		}
		var payload struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal(lines[i].Item, &payload); err != nil {
			t.Fatal(err)
		}
		types = append(types, payload.Type+"/"+payload.Role)
	}
	return types
}

func completedItemCountFromRollout(t *testing.T, path string) int {
	t.Helper()
	lines, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for i := range lines {
		if lines[i].Type != "event_msg" {
			continue
		}
		var payload struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(lines[i].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type == "item_completed" {
			count++
		}
		if payload.Type == "item_started" {
			count++
		}
	}
	return count
}

func marshalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
