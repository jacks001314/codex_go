package rollout

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
)

// TestPaginatedRolloutPersistsConfigurationUpdateLikeRust covers the paginated
// path: harness-authored configuration updates have no core TurnItem variant,
// so they persist as trusted response_item lines (Rust #43110).
func TestPaginatedRolloutPersistsConfigurationUpdateLikeRust(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:   t.TempDir(),
		SessionID:   "session-config-update",
		ThreadID:    "thread-config-update",
		CWD:         "D:/repo",
		Now:         now,
		HistoryMode: "paginated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !recorder.IsPaginated() {
		t.Fatal("recorder is not paginated; test would not cover the encoder")
	}
	items := []session.Item{{
		ID:        "configuration-update-turn-1-1",
		Type:      "configuration_update",
		CreatedAt: now,
		Data: map[string]any{
			"reasoning":        map[string]any{"effort": "high"},
			"harness_metadata": json.RawMessage(`{"harness_authored_configuration":true}`),
		},
		Metadata: map[string]any{"turnId": "turn-1"},
	}}
	if err := AppendSessionItems(recorder, items, now); err != nil {
		t.Fatalf("AppendSessionItems error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range record.Items {
		if record.Items[i].Type != "configuration_update" {
			continue
		}
		found = true
		reasoning, _ := record.Items[i].Data["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("reasoning = %#v", reasoning)
		}
		if session.InputItemFromItem(&record.Items[i], nil) == nil {
			t.Fatalf("replayed configuration update lost its harness provenance: %#v", record.Items[i])
		}
	}
	if !found {
		t.Fatalf("configuration_update missing from the paginated rollout: %#v", record.Items)
	}
}
