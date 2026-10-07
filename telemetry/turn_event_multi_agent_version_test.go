package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #51333 (5ddd19e8a9, analytics/src/tests/suite/turns/events_tests.rs
// `turn_event_serializes_expected_shape`): the resolved turn version reaches the
// turn event as `multi_agent_version`, and MultiAgentVersion::V2 serializes as
// the snake_case `"v2"`.
func TestCodexTurnEventMultiAgentVersionLikeRust(t *testing.T) {
	event := NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID:          "thread-version",
		SessionID:         "session-thread-version",
		TurnID:            "turn-version",
		MultiAgentVersion: MultiAgentVersionV2,
	})
	if event.EventParams.MultiAgentVersion != "v2" {
		t.Fatalf("multi_agent_version = %q, want v2", event.EventParams.MultiAgentVersion)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	if !strings.Contains(string(raw), `"multi_agent_version":"v2"`) {
		t.Fatalf("serialized event = %s, want a multi_agent_version of v2", raw)
	}

	// A turn that never resolved a multi-agent surface reports Rust's Disabled
	// arm, which serializes as "disabled".
	disabled := NewCodexTurnEvent(CodexTurnEventInput{
		ThreadID:  "thread-no-agents",
		SessionID: "session-thread-no-agents",
		TurnID:    "turn-no-agents",
	})
	if disabled.EventParams.MultiAgentVersion != MultiAgentVersionDisabled {
		t.Fatalf("multi_agent_version = %q, want disabled", disabled.EventParams.MultiAgentVersion)
	}

	for _, testCase := range []struct {
		name string
		in   string
		want string
	}{
		{name: "disabled arm", in: MultiAgentVersionDisabled, want: "disabled"},
		{name: "v1 arm", in: MultiAgentVersionV1, want: "v1"},
		{name: "v2 arm", in: MultiAgentVersionV2, want: "v2"},
		{name: "unresolved falls back to disabled", in: "  ", want: "disabled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value := multiAgentVersionValue(testCase.in)
			if value != testCase.want {
				t.Fatalf("multiAgentVersionValue(%q) = %q, want %q", testCase.in, value, testCase.want)
			}
		})
	}
}
