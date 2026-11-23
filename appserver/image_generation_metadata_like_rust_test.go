package appserver

import (
	"encoding/json"
	"testing"
)

// Rust #62871: app-server image-generation items expose the backend's
// per-image metadata as optional `imageMetadata` on both the file-backed and
// inline variants, read from the stored `imageMetadata`/`image_metadata` keys.
func TestImageGenerationItemSerializesImageMetadataLikeRust(t *testing.T) {
	want := `{"entries":[{"name":"first","value":1.5}],"futureKey":{"nestedKey":[0,false,null]}}`
	metadata := map[string]any{
		"entries":   []any{map[string]any{"name": "first", "value": 1.5}},
		"futureKey": map[string]any{"nestedKey": []any{0, false, nil}},
	}
	fileItem := ThreadItem{
		Type:   "imageGeneration",
		ID:     "call-file",
		Status: "completed",
		Data: map[string]any{
			"fileId":        "file_generated",
			"imageMetadata": metadata,
		},
	}
	payload := marshalThreadItemObject(t, &fileItem)
	if payload["fileId"] != "file_generated" {
		t.Fatalf("fileId = %#v", payload["fileId"])
	}
	if got := marshalMetadataForAppserverTest(t, payload["imageMetadata"]); got != want {
		t.Fatalf("file imageMetadata = %s, want %s", got, want)
	}

	alias := ThreadItem{
		Type: "imageGeneration",
		ID:   "call-alias",
		Data: map[string]any{
			"result":         "aW5saW5l",
			"image_metadata": metadata,
		},
	}
	aliasPayload := marshalThreadItemObject(t, &alias)
	if aliasPayload["result"] != "aW5saW5l" {
		t.Fatalf("alias result = %#v", aliasPayload["result"])
	}
	if got := marshalMetadataForAppserverTest(t, aliasPayload["imageMetadata"]); got != want {
		t.Fatalf("alias imageMetadata = %s, want %s", got, want)
	}

	// Empty or malformed metadata is omitted while the image item survives.
	for _, invalid := range []any{
		map[string]any{},
		[]any{1},
		"text",
		5,
		nil,
	} {
		item := ThreadItem{
			Type: "imageGeneration",
			ID:   "call-invalid",
			Data: map[string]any{"result": "aW5saW5l", "imageMetadata": invalid},
		}
		got := marshalThreadItemObject(t, &item)
		if _, ok := got["imageMetadata"]; ok {
			t.Fatalf("invalid metadata %#v serialized: %#v", invalid, got["imageMetadata"])
		}
		if got["result"] != "aW5saW5l" {
			t.Fatalf("invalid metadata %#v dropped the image: %#v", invalid, got)
		}
	}
}

// Legacy history replay normalizes the snake_case field onto the item data
// before the wire encoder reads it.
func TestHistoryImageMetadataAliasLikeRust(t *testing.T) {
	data := normalizeHistoryUnionData(map[string]any{
		"image_metadata": map[string]any{"futureKey": true},
	}, "")
	metadata, ok := data["imageMetadata"].(map[string]any)
	if !ok || metadata["futureKey"] != true {
		t.Fatalf("normalized imageMetadata = %#v", data["imageMetadata"])
	}
}

func marshalMetadataForAppserverTest(t *testing.T, value any) string {
	t.Helper()
	metadata, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v, want an object", value)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal(metadata) error = %v", err)
	}
	return string(encoded)
}
