package state

// Mirrors Rust #62251: a persisted model user message annotated with realtime
// provenance also lands in the realtime stream at its own rollout ordinal, the
// paginated timeline keeps hiding that duplicate of the user message, and the
// stored observation time restores the realtime clock after a restart.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
)

func appendProvenanceResponseItem(t *testing.T, recorder *rollout.Recorder, id string, turnID string, text string, provenance map[string]any, at time.Time) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type": "message",
		"id":   id,
		"role": "user",
		"content": []map[string]any{
			{"type": "input_text", "text": text},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{"turn_id": turnID},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendLine(rollout.Line{
		Type:         "response_item",
		Timestamp:    at.Format(time.RFC3339Nano),
		Item:         payload,
		ItemMetadata: metadata,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResponseItemReceiptsEnterRealtimeHistoryLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	threadID := "receipt-thread"
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: threadID, ThreadID: threadID, CWD: filepath.Join(home, "repo"),
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStartedWithRoot("", "turn-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	appendProvenanceResponseItem(t, recorder, "user-handoff", "turn-1", "start the long task", map[string]any{
		"realtime_handoff": map[string]any{
			"event_id":            "voice:handoff",
			"realtime_session_id": "voice",
			"observed_at_ms":      4_200,
			"handoff_id":          "handoff-1",
			"mode":                "start",
			"source_event_ids":    []string{"event-a"},
		},
	}, now.Add(2*time.Second))
	appendProvenanceResponseItem(t, recorder, "user-chunk", "", "checking staging", map[string]any{
		"realtime_session_input": map[string]any{
			"event_id":            "voice:chunk",
			"realtime_session_id": "voice",
			"observed_at_ms":      4_500,
			"source_event_ids":    []string{"event-b"},
		},
	}, now.Add(3*time.Second))
	if err := recorder.AppendTurnCompleteWithRoot("", "turn-1", now.Add(4*time.Second), 3000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	page, err := runtime.ListThreadRealtime(ctx, ThreadRealtimeListParams{ThreadID: threadID, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Coverage != ThreadRealtimeCoverageReady || len(page.Data) != 2 {
		t.Fatalf("realtime coverage/items = %v/%d, want ready/2", page.Coverage, len(page.Data))
	}
	wantTypes := []string{"turn_input_accepted", "session_input_saved"}
	wantObserved := []int64{4_200, 4_500}
	wantIDs := []string{"voice:handoff", "voice:chunk"}
	for index, entry := range page.Data {
		var tagged struct {
			Type       string   `json:"type"`
			Reason     string   `json:"reason"`
			HandoffID  string   `json:"handoff_id"`
			TurnID     string   `json:"turn_id"`
			InputItem  string   `json:"input_item_id"`
			Input      string   `json:"input"`
			SourceIDs  []string `json:"source_event_ids"`
			ID         string   `json:"id"`
			SessionID  string   `json:"realtime_session_id"`
			ObservedAt int64    `json:"observed_at_ms"`
		}
		if err := json.Unmarshal(entry.ItemJSON, &tagged); err != nil {
			t.Fatalf("decode receipt %d: %v", index, err)
		}
		if tagged.Type != wantTypes[index] || tagged.ID != wantIDs[index] || tagged.SessionID != "voice" || tagged.ObservedAt != wantObserved[index] {
			t.Fatalf("receipt %d = %#v, want %s at %d", index, tagged, wantTypes[index], wantObserved[index])
		}
		if entry.ObservedAtMS == nil || *entry.ObservedAtMS != wantObserved[index] || entry.ItemJSON == nil {
			t.Fatalf("receipt %d position metadata = %#v", index, entry)
		}
		if index == 0 && (tagged.HandoffID != "handoff-1" || tagged.TurnID != "turn-1" || tagged.InputItem != "user-handoff" || tagged.Input != "start the long task") {
			t.Fatalf("handoff receipt = %#v", tagged)
		}
		if index == 1 && (tagged.Reason != "chunk" || tagged.InputItem != "user-chunk" || tagged.Input != "checking staging") {
			t.Fatalf("chunk receipt = %#v", tagged)
		}
	}
	if page.Data[1].Position <= page.Data[0].Position {
		t.Fatalf("receipt positions = %d, %d; want rollout order", page.Data[0].Position, page.Data[1].Position)
	}
	// The paginated timeline still keeps the receipt rows out: the user message
	// remains the single visible copy, exactly as Rust's timeline query does.
	timeline, err := runtime.ListThreadTimeline(ctx, ThreadTimelineListParams{ThreadID: threadID, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range timeline.Entries {
		if entry.Kind != ThreadTimelineRealtimeItem {
			continue
		}
		var tagged struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(entry.RealtimeItemJSON, &tagged); err != nil {
			t.Fatal(err)
		}
		if tagged.Type == "turn_input_accepted" || tagged.Type == "session_input_saved" {
			t.Fatalf("timeline leaked receipt %q", tagged.Type)
		}
	}

	// The stored observation time restores the clock even though the wall clock
	// is far behind it.
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	floor := rollout.ObservationClockFloor(lines)
	if floor == nil || *floor != 4_500 {
		t.Fatalf("observation clock floor = %v, want 4500", floor)
	}
}
