package telemetry

import (
	"encoding/json"
	"testing"
)

// Rust #62357 serializes deduplicated reasons only for incomplete captures.
func TestCodexTurnEventSerializesIncompleteCriticalPathReasonsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		path    CodexTurnCriticalPath
		want    []any
		present bool
	}{
		{
			name:    "disabled",
			path:    CodexTurnCriticalPath{Status: CodexTurnCriticalPathStatusDisabled},
			present: false,
		},
		{
			name: "incomplete",
			path: CodexTurnCriticalPath{
				Status:            CodexTurnCriticalPathStatusIncomplete,
				IncompleteReasons: []string{"code_mode_unsupported", "unattributed_user_wait"},
			},
			want:    []any{"code_mode_unsupported", "unattributed_user_wait"},
			present: true,
		},
		{
			name: "complete ignores reasons",
			path: CodexTurnCriticalPath{
				Status:            CodexTurnCriticalPathStatusComplete,
				Durations:         &CodexTurnCriticalPathDurations{},
				IncompleteReasons: []string{"forced_teardown"},
			},
			present: false,
		},
	} {
		event := NewCodexTurnEvent(CodexTurnEventInput{
			ThreadID:      "thread-reasons",
			SessionID:     "session-reasons",
			TurnID:        "turn-reasons",
			Status:        stringPtrTelemetry("completed"),
			TimingProfile: CodexTurnTimingProfile{CriticalPath: testCase.path},
		})
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("%s: marshal error = %v", testCase.name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatalf("%s: unmarshal error = %v", testCase.name, err)
		}
		params, _ := payload["event_params"].(map[string]any)
		reasons, present := params["critical_path_incomplete_reasons"]
		if present != testCase.present {
			t.Fatalf("%s: reasons present = %t, want %t (%#v)", testCase.name, present, testCase.present, params)
		}
		if testCase.present {
			got, _ := reasons.([]any)
			if len(got) != len(testCase.want) {
				t.Fatalf("%s: reasons = %#v, want %#v", testCase.name, got, testCase.want)
			}
			for index := range testCase.want {
				if got[index] != testCase.want[index] {
					t.Fatalf("%s: reasons = %#v, want %#v", testCase.name, got, testCase.want)
				}
			}
		}
	}
}
