package rollout

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
)

// TestRolloutPreservesAdditionalToolsInBothHistoryModes mirrors Rust #50435:
// additional_tools response items survive recording and loading in both legacy
// and paginated history modes without parse errors, keeping their tool
// definitions.
func TestRolloutPreservesAdditionalToolsInBothHistoryModes(t *testing.T) {
	tools := []any{map[string]any{
		"type":        "namespace",
		"name":        "functions",
		"description": "Available tools.",
		"tools": []any{map[string]any{
			"type":        "function",
			"name":        "lookup",
			"description": "Look up a value.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		}},
	}}
	raw, err := json.Marshal(map[string]any{
		"type":  "additional_tools",
		"role":  "developer",
		"tools": tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"legacy", "paginated"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC)
			recorder, err := NewRecorder(&CreateParams{
				CodexHome:   t.TempDir(),
				SessionID:   "session-additional-tools-" + mode,
				ThreadID:    "thread-additional-tools-" + mode,
				CWD:         "D:/repo",
				Now:         now,
				HistoryMode: mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			items := []session.Item{{
				ID:        "at-1",
				Type:      "additional_tools",
				Role:      "developer",
				Raw:       raw,
				CreatedAt: now,
				Data:      map[string]any{"tools": tools},
				Metadata:  map[string]any{"turnId": "turn-1"},
			}}
			if err := AppendSessionItems(recorder, items, now); err != nil {
				t.Fatalf("AppendSessionItems(%s) error = %v", mode, err)
			}
			path := recorder.Path()
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}
			record, err := RecordFromPath(path, false)
			if err != nil {
				t.Fatalf("RecordFromPath(%s) error = %v", mode, err)
			}
			found := false
			for i := range record.Items {
				if record.Items[i].Type != "additional_tools" {
					continue
				}
				found = true
				if _, ok := record.Items[i].Data["tools"].([]any); !ok {
					t.Fatalf("%s: additional_tools lost its tool definitions: %#v", mode, record.Items[i])
				}
			}
			if !found {
				t.Fatalf("%s: additional_tools missing from the rollout: %#v", mode, record.Items)
			}
		})
	}
}
