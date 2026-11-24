package codemode

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDedupeMCPTextFallbackLikeRust pins Rust #63540's comparison: only an
// original successful MCP result whose single content block is a text fallback
// equal to structuredContent loses content from the printed JSON.
func TestDedupeMCPTextFallbackLikeRust(t *testing.T) {
	duplicate := map[string]any{
		"success":           true,
		"output":            "{\"a\":1}",
		"content":           []any{map[string]any{"type": "text", "text": "{\"a\":1}"}},
		"structuredContent": map[string]any{"a": float64(1)},
	}
	deduped, ok := dedupeMCPTextFallback(duplicate)
	if !ok {
		t.Fatal("duplicate fallback was not deduplicated")
	}
	printed := deduped.(map[string]any)
	if _, present := printed["content"]; present {
		t.Fatalf("printed result kept content: %#v", printed)
	}
	if printed["structuredContent"] == nil || printed["output"] != duplicate["output"] {
		t.Fatalf("printed result lost other fields: %#v", printed)
	}

	cases := map[string]map[string]any{
		"different fallback": {
			"success":           true,
			"content":           []any{map[string]any{"type": "text", "text": "{\"a\":2}"}},
			"structuredContent": map[string]any{"a": float64(1)},
		},
		"failed result": {
			"success":           false,
			"content":           []any{map[string]any{"type": "text", "text": "{\"a\":1}"}},
			"structuredContent": map[string]any{"a": float64(1)},
		},
		"two content blocks": {
			"success": true,
			"content": []any{
				map[string]any{"type": "text", "text": "{\"a\":1}"},
				map[string]any{"type": "text", "text": "extra"},
			},
			"structuredContent": map[string]any{"a": float64(1)},
		},
		"image block": {
			"success":           true,
			"content":           []any{map[string]any{"type": "image", "data": "x"}},
			"structuredContent": map[string]any{"a": float64(1)},
		},
		"no structured content": {
			"success": true,
			"content": []any{map[string]any{"type": "text", "text": "{\"a\":1}"}},
		},
		"invalid fallback json": {
			"success":           true,
			"content":           []any{map[string]any{"type": "text", "text": "not json"}},
			"structuredContent": map[string]any{"a": float64(1)},
		},
	}
	for name, value := range cases {
		if got, changed := dedupeMCPTextFallback(value); changed || got.(map[string]any)["content"] == nil {
			t.Fatalf("%s was deduplicated: %#v", name, got)
		}
	}
}

// TestSobekTextOmitsDuplicateMCPFallbackLikeRust runs text() on an MCP-shaped
// result with the option on and off: the JavaScript value keeps both fields and
// only the printed JSON changes.
func TestSobekTextOmitsDuplicateMCPFallbackLikeRust(t *testing.T) {
	source := `const result = {success: true, output: '{"a":1}', content: [{type: "text", text: '{"a":1}'}], structuredContent: {a: 1}};
text(result);
text(result.content.length);`

	for _, test := range []struct {
		name   string
		dedup  bool
		hasKey bool
	}{
		{name: "flag off keeps the fallback", dedup: false, hasKey: true},
		{name: "flag on omits the fallback", dedup: true, hasKey: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewSobekEngine().Execute(t.Context(), EngineRequest{Source: source, MCPResultDedup: test.dedup})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(result.ContentItems) != 2 {
				t.Fatalf("items = %#v", result.ContentItems)
			}
			var printed map[string]any
			if err := json.Unmarshal([]byte(result.ContentItems[0].Text), &printed); err != nil {
				t.Fatalf("printed text is not JSON: %v (%q)", err, result.ContentItems[0].Text)
			}
			_, present := printed["content"]
			if present != test.hasKey {
				t.Fatalf("content present = %v, want %v (%q)", present, test.hasKey, result.ContentItems[0].Text)
			}
			if printed["structuredContent"] == nil {
				t.Fatalf("printed result lost structuredContent: %q", result.ContentItems[0].Text)
			}
			if strings.TrimSpace(result.ContentItems[1].Text) != "1" {
				t.Fatalf("JavaScript content length = %q, want 1", result.ContentItems[1].Text)
			}
		})
	}
}

// TestOpenSessionRequestCarriesMCPResultDedupLikeRust keeps the option on the
// wire only when it was requested.
func TestOpenSessionRequestCarriesMCPResultDedupLikeRust(t *testing.T) {
	off := OpenSessionRequestWithOptions("session-1", SessionOptions{})
	if off.MCPResultDedup != nil {
		t.Fatalf("default session carried %v", *off.MCPResultDedup)
	}
	on := OpenSessionRequestWithOptions("session-1", SessionOptions{MCPResultDedup: true})
	if on.MCPResultDedup == nil || !*on.MCPResultDedup {
		t.Fatal("dedup session did not carry the option")
	}
	encoded, err := json.Marshal(on)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded HostRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.MCPResultDedup == nil || !*decoded.MCPResultDedup {
		t.Fatalf("round trip lost the option: %s", encoded)
	}
}

// TestSessionHostCapturesMCPResultDedupLikeRust fixes the option when the
// session opens.
func TestSessionHostCapturesMCPResultDedupLikeRust(t *testing.T) {
	host := NewSessionHost(NewSessionRuntime())
	dedup := true
	if _, _, err := host.Handle(t.Context(), &HostRequest{Method: "session/open", SessionID: "session-1", MCPResultDedup: &dedup}, nil); err != nil {
		t.Fatalf("Handle(open) error = %v", err)
	}
	if !host.mcpResultDedup || !host.runtime.mcpResultDedup {
		t.Fatal("session host did not capture the dedup option")
	}
}
