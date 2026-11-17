package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestModelInfoPreservesMultiAgentToolsInCodeModePresenceLikeRust pins Rust
// #61854 protocol/src/openai_models.rs: the flag is optional, absent and null
// both mean "fall back to the runtime setting", and an explicit false must
// survive a round trip instead of collapsing into absence.
func TestModelInfoPreservesMultiAgentToolsInCodeModePresenceLikeRust(t *testing.T) {
	base := ModelInfo{Slug: "probe-model"}
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	if strings.Contains(string(data), "multi_agent_tools_in_code_mode") {
		t.Fatalf("absent flag was serialized: %s", data)
	}
	var roundTripped ModelInfo
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal model: %v", err)
	}
	if roundTripped.MultiAgentToolsInCodeMode != nil {
		t.Fatalf("absent flag decoded as %v", *roundTripped.MultiAgentToolsInCodeMode)
	}

	cases := []struct {
		raw  string
		want *bool
	}{
		{raw: "null", want: nil},
		{raw: "false", want: boolPtr(false)},
		{raw: "true", want: boolPtr(true)},
	}
	for _, tc := range cases {
		var decoded ModelInfo
		if err := json.Unmarshal([]byte(`{"slug":"probe-model","multi_agent_tools_in_code_mode":`+tc.raw+`}`), &decoded); err != nil {
			t.Fatalf("decode %s: %v", tc.raw, err)
		}
		if (tc.want == nil) != (decoded.MultiAgentToolsInCodeMode == nil) {
			t.Fatalf("decode %s presence = %v", tc.raw, decoded.MultiAgentToolsInCodeMode)
		}
		if tc.want != nil && *decoded.MultiAgentToolsInCodeMode != *tc.want {
			t.Fatalf("decode %s = %v", tc.raw, *decoded.MultiAgentToolsInCodeMode)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("re-encode %s: %v", tc.raw, err)
		}
		if tc.want == nil && strings.Contains(string(encoded), "multi_agent_tools_in_code_mode") {
			t.Fatalf("null flag was serialized: %s", encoded)
		}
		if tc.want != nil && !strings.Contains(string(encoded), `"multi_agent_tools_in_code_mode":`+tc.raw) {
			t.Fatalf("explicit %s flag was dropped: %s", tc.raw, encoded)
		}
	}

	// The catalog decodes through a hand-written unmarshaler, so the sibling
	// multi-agent metadata field has to be wired there as well.
	var withEffort ModelInfo
	if err := json.Unmarshal([]byte(`{"slug":"probe-model","multi_agent_reasoning_effort":"high"}`), &withEffort); err != nil {
		t.Fatalf("decode reasoning effort: %v", err)
	}
	if withEffort.MultiAgentReasoningEffort == nil || *withEffort.MultiAgentReasoningEffort != "high" {
		t.Fatalf("multi_agent_reasoning_effort = %v", withEffort.MultiAgentReasoningEffort)
	}
}

func boolPtr(value bool) *bool { return &value }
