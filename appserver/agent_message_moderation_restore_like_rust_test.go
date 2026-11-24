package appserver

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #60172: a moderation verdict restored from item data keeps the
// nullable display the app-server contract requires, and an unreadable stored
// instruction leaves the answer restricted with no display.
func TestRestoredAgentMessageModerationKeepsNullableDisplayLikeRust(t *testing.T) {
	cases := []struct {
		name          string
		stored        map[string]any
		wantModerated bool
		wantDisplay   map[string]any
	}{
		{
			name:          "absent display",
			stored:        map[string]any{"moderated": false},
			wantModerated: false,
		},
		{
			name: "unreadable display",
			stored: map[string]any{
				"moderated": false,
				"display":   map[string]any{"action": "answerHelpline"},
			},
			wantModerated: true,
		},
		{
			name: "readable display",
			stored: map[string]any{
				"moderated": true,
				"display": map[string]any{
					"version": 1,
					"mode":    "block",
					"reason":  map[string]any{"kind": "protection", "protectionType": "self_harm"},
				},
			},
			wantModerated: true,
			wantDisplay: map[string]any{
				"version": 1,
				"mode":    "block",
				"reason":  map[string]any{"kind": "protection", "protectionType": "self_harm"},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			item := &ThreadItem{
				ID: "msg-1", Type: "agentMessage", Text: "hi",
				Data: map[string]any{"moderation": testCase.stored},
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			moderation, ok := payload["moderation"].(map[string]any)
			if !ok {
				t.Fatalf("moderation missing: %s", encoded)
			}
			if moderated, _ := moderation["moderated"].(bool); moderated != testCase.wantModerated {
				t.Fatalf("moderated = %v, want %v: %s", moderation["moderated"], testCase.wantModerated, encoded)
			}
			display, present := moderation["display"]
			if !present {
				t.Fatalf("display field missing from the contract: %s", encoded)
			}
			if testCase.wantDisplay == nil {
				if display != nil {
					t.Fatalf("display = %#v, want null: %s", display, encoded)
				}
				return
			}
			if got, _ := display.(map[string]any); !jsonEqual(got, testCase.wantDisplay) {
				t.Fatalf("display = %#v, want %#v", display, testCase.wantDisplay)
			}
		})
	}
}

func jsonEqual(left map[string]any, right map[string]any) bool {
	leftJSON, errLeft := json.Marshal(left)
	rightJSON, errRight := json.Marshal(right)
	return errLeft == nil && errRight == nil && string(leftJSON) == string(rightJSON)
}
