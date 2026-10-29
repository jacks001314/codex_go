package model

import (
	"encoding/json"
	"testing"
)

// TestUsageMetadataFromStreamEventDataPreservesRawUsageLikeRust mirrors Rust
// #41980: the complete upstream `response.usage` object is carried in
// ResponseUsageMetadata.metadata while the existing amount field is retained.
func TestUsageMetadataFromStreamEventDataPreservesRawUsageLikeRust(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"id":"resp-1","usage_metadata":{"amount":"0.25"},"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":2}}}}`)
	metadata, ok := usageMetadataFromStreamEventData(data)
	if !ok || metadata == nil {
		t.Fatalf("usageMetadataFromStreamEventData() = %v, %v", metadata, ok)
	}
	if metadata.Amount == nil || *metadata.Amount != "0.25" {
		t.Fatalf("Amount = %v", metadata.Amount)
	}
	var raw map[string]any
	if err := json.Unmarshal(metadata.Metadata, &raw); err != nil {
		t.Fatalf("raw usage is not valid JSON: %v (%s)", err, metadata.Metadata)
	}
	if raw["total_tokens"] != float64(15) || raw["input_tokens"] != float64(10) || raw["output_tokens"] != float64(5) {
		t.Fatalf("raw usage = %#v", raw)
	}
	details, _ := raw["input_tokens_details"].(map[string]any)
	if details["cached_tokens"] != float64(2) {
		t.Fatalf("raw usage details = %#v", raw)
	}

	// A response without usage_metadata still reports the raw usage object.
	data = []byte(`{"type":"response.completed","response":{"id":"resp-2","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`)
	metadata, ok = usageMetadataFromStreamEventData(data)
	if !ok || metadata == nil || metadata.Amount != nil {
		t.Fatalf("metadata without amount = %#v, %v", metadata, ok)
	}
	if err := json.Unmarshal(metadata.Metadata, &raw); err != nil || raw["total_tokens"] != float64(3) {
		t.Fatalf("raw usage = %#v (%v)", raw, err)
	}

	// A null usage object contributes nothing, matching Rust's filter.
	data = []byte(`{"type":"response.completed","response":{"id":"resp-3","usage":null}}`)
	if metadata, ok := usageMetadataFromStreamEventData(data); ok || metadata != nil {
		t.Fatalf("null usage produced metadata = %#v", metadata)
	}
}
