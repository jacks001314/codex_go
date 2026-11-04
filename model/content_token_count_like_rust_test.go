package model

import (
	"encoding/json"
	"testing"
)

func uint64PtrForTest(value uint64) *uint64 { return &value }

// Rust #59041 reads the producer-supplied content-token count leniently: a
// missing, malformed or non-numeric value is unavailable rather than fatal.
func TestContentTokenCountFromInternalMetadataLikeRust(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want *uint64
	}{
		{name: "missing", raw: `{}`},
		{name: "empty", raw: ``},
		{name: "null", raw: `{"content_token_count":null}`},
		{name: "zero", raw: `{"content_token_count":0}`, want: uint64PtrForTest(0)},
		{name: "value", raw: `{"content_token_count":4321}`, want: uint64PtrForTest(4321)},
		{name: "string", raw: `{"content_token_count":"4321"}`},
		{name: "negative", raw: `{"content_token_count":-1}`},
		{name: "fraction", raw: `{"content_token_count":1.5}`},
		{name: "malformed envelope", raw: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := contentTokenCountFromInternalMetadata(json.RawMessage(test.raw))
			if test.want == nil {
				if got != nil {
					t.Fatalf("contentTokenCountFromInternalMetadata(%s) = %d, want nil", test.raw, *got)
				}
				return
			}
			if got == nil || *got != *test.want {
				t.Fatalf("contentTokenCountFromInternalMetadata(%s) = %v, want %d", test.raw, got, *test.want)
			}
		})
	}
}

// Completion telemetry must not reach stored items or later model requests.
func TestAgentItemContentTokenCountIsNotSerialized(t *testing.T) {
	item := AgentItem{ID: "msg-1", Type: "agent_message", Text: "done", ContentTokenCount: uint64PtrForTest(7)}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["ContentTokenCount"]; ok {
		t.Fatalf("serialized agent item leaked ContentTokenCount: %s", raw)
	}
	if _, ok := decoded["content_token_count"]; ok {
		t.Fatalf("serialized agent item leaked content_token_count: %s", raw)
	}
}
