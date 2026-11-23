package rollout

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"codex_go/session"
)

// Rust #62871 keeps a legacy record's `image_metadata` when the canonicalizer
// re-encodes the image-generation event as an Extension completed item.
func TestCanonicalizeRolloutPreservesLegacyImageGenerationMetadataLikeRust(t *testing.T) {
	source := `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i1","status":"completed","result":"cG5n","generation_id":"gen-1","image_metadata":{"entries":[{"name":"first","value":1.5}],"futureKey":{"nestedKey":[0,false,null]}}}}
`
	home, path := writeCanonicalMigrationRawRollout(t, source)
	if _, err := CanonicalizeRolloutWithStats(home, path); err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := `"imageMetadata":{"entries":[{"name":"first","value":1.5}],"futureKey":{"nestedKey":[0,false,null]}}`
	if !strings.Contains(string(got), want) {
		t.Fatalf("migrated rollout lost image metadata:\n%s", got)
	}
}

// A malformed value is dropped by the typed decode, yet the image event still
// migrates and an empty map is omitted, mirroring `DefaultOnError` plus
// `skip_serializing_if = "Map::is_empty"`.
func TestCanonicalizeRolloutDropsInvalidImageGenerationMetadataLikeRust(t *testing.T) {
	source := `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i1","status":"completed","result":"cG5n","image_metadata":[]}}
`
	home, path := writeCanonicalMigrationRawRollout(t, source)
	if _, err := CanonicalizeRolloutWithStats(home, path); err != nil {
		t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(got)
	if strings.Contains(text, "imageMetadata") {
		t.Fatalf("invalid metadata was migrated:\n%s", text)
	}
	if !strings.Contains(text, `"kind":"image_gen.generation"`) || !strings.Contains(text, `"result":"cG5n"`) {
		t.Fatalf("invalid metadata dropped the image item:\n%s", text)
	}
}

// Replaying a modern completed item keeps its imageMetadata unless the value
// is not a JSON object.
func TestCanonicalExtensionImageGenerationReplaysMetadataLikeRust(t *testing.T) {
	valid := json.RawMessage(`{"type":"Extension","kind":"image_gen.generation","id":"i1","status":"completed","revisedPrompt":null,"result":"r","transparentBackground":null,"failure":null,"generationId":null,"imageMetadata":{"futureKey":{"nestedKey":[0,false,null]}}}`)
	got, ok, err := canonicalExtensionImageGeneration(valid, "image_gen.generation")
	if err != nil || !ok {
		t.Fatalf("canonicalExtensionImageGeneration() = ok %v, err %v", ok, err)
	}
	if want := `"imageMetadata":{"futureKey":{"nestedKey":[0,false,null]}}`; !strings.Contains(string(got), want) {
		t.Fatalf("replayed item lost metadata: %s", got)
	}

	invalid := json.RawMessage(`{"type":"Extension","kind":"image_gen.generation","id":"i2","status":"completed","revisedPrompt":null,"result":"r","transparentBackground":null,"failure":null,"generationId":null,"imageMetadata":5}`)
	got, ok, err = canonicalExtensionImageGeneration(invalid, "image_gen.generation")
	if err != nil || !ok {
		t.Fatalf("invalid metadata item dropped: ok %v, err %v", ok, err)
	}
	if strings.Contains(string(got), "imageMetadata") {
		t.Fatalf("invalid metadata replayed: %s", got)
	}
}

// New completions persist the metadata in the core Extension item and expose
// it again on the public imageGeneration item.
func TestTurnItemImageGenerationMetadataRoundTripLikeRust(t *testing.T) {
	item := &session.Item{
		ID:     "img-1",
		Type:   "imageGeneration",
		Status: "completed",
		Data: map[string]any{
			"result": "cG5n",
			"imageMetadata": map[string]any{
				"futureKey": map[string]any{"nestedKey": []any{0, false, nil}},
			},
		},
		Metadata: map[string]any{"turnId": "t1"},
	}
	core, turnID, err := CoreTurnItemJSONFromSessionItem(item)
	if err != nil {
		t.Fatalf("CoreTurnItemJSONFromSessionItem: %v", err)
	}
	if turnID != "t1" {
		t.Fatalf("turnID = %q", turnID)
	}
	if want := `"image_metadata":{"futureKey":{"nestedKey":[0,false,null]}}`; !strings.Contains(string(core), want) {
		t.Fatalf("core item lost metadata: %s", core)
	}
	public, _, publicType, err := PublicThreadItemJSONFromCore(core)
	if err != nil {
		t.Fatalf("PublicThreadItemJSONFromCore: %v", err)
	}
	if publicType != "imageGeneration" {
		t.Fatalf("public type = %q", publicType)
	}
	if want := `"imageMetadata":{"futureKey":{"nestedKey":[0,false,null]}}`; !strings.Contains(string(public), want) {
		t.Fatalf("public item lost metadata: %s", public)
	}
}
