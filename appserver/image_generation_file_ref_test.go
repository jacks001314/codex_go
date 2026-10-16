package appserver

import (
	"encoding/json"
	"testing"
)

// wireInputImage mirrors the completed item's `inputImages` element
// (Rust #54420 `ImageGenerationInputImage`), used to exercise the typed-slice
// path the in-process image-generation handler produces.
type wireInputImage struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func marshalThreadItemObject(t *testing.T, item *ThreadItem) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", item.ID, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", item.ID, err)
	}
	return payload
}

// TestImageGenerationItemSerializesFileReferenceLikeRust freezes the app-server
// wire shape of Rust #54398 (ext/items/src/image_generation.rs
// `GeneratedImageRef`): the item serializes `fileId` when a durable attachment
// reference exists, keeps `result` for inline items, prefers the file reference
// when a record carries both fields (the `File` variant is declared first for
// migration-era records), and omits `inputImages` when it is empty (Rust #54420).
func TestImageGenerationItemSerializesFileReferenceLikeRust(t *testing.T) {
	both := ThreadItem{
		Type:   "imageGeneration",
		ID:     "call-image",
		Status: "completed",
		Data: map[string]any{
			"fileId":        "file_generated",
			"result":        "aW5saW5l",
			"revisedPrompt": "draw a square",
			"inputImages":   []any{map[string]any{"path": "/tmp/a.png", "sha256": "abc"}},
		},
	}
	payload := marshalThreadItemObject(t, &both)
	if got := payload["fileId"]; got != "file_generated" {
		t.Fatalf("fileId = %#v, want file_generated", got)
	}
	if _, ok := payload["result"]; ok {
		t.Fatalf("file-backed item serialized the inline result: %#v", payload)
	}
	images, ok := payload["inputImages"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("inputImages = %#v, want one entry", payload["inputImages"])
	}
	first, ok := images[0].(map[string]any)
	if !ok || first["path"] != "/tmp/a.png" || first["sha256"] != "abc" {
		t.Fatalf("inputImages[0] = %#v", images[0])
	}

	// The snake_case alias used by in-process item data is read the same way.
	alias := ThreadItem{
		Type: "imageGeneration",
		ID:   "call-alias",
		Data: map[string]any{"file_id": "file_alias"},
	}
	if got := marshalThreadItemObject(t, &alias)["fileId"]; got != "file_alias" {
		t.Fatalf("file_id alias = %#v, want file_alias", got)
	}

	// A typed slice (the handler's own type) re-encodes to the same array.
	typed := ThreadItem{
		Type: "imageGeneration",
		ID:   "call-typed",
		Data: map[string]any{
			"result":      "aW5saW5l",
			"inputImages": []wireInputImage{{Path: "/tmp/b.png", SHA256: "def"}},
		},
	}
	typedPayload := marshalThreadItemObject(t, &typed)
	if _, ok := typedPayload["fileId"]; ok {
		t.Fatalf("inline item produced a fileId: %#v", typedPayload)
	}
	if got := typedPayload["result"]; got != "aW5saW5l" {
		t.Fatalf("inline result = %#v, want the base64 payload", got)
	}
	typedImages, ok := typedPayload["inputImages"].([]any)
	if !ok || len(typedImages) != 1 {
		t.Fatalf("typed inputImages = %#v", typedPayload["inputImages"])
	}
	typedFirst := typedImages[0].(map[string]any)
	if typedFirst["path"] != "/tmp/b.png" || typedFirst["sha256"] != "def" {
		t.Fatalf("typed inputImages[0] = %#v", typedImages[0])
	}

	// No local input read: the field is absent, exactly like Rust's
	// `skip_serializing_if = "Vec::is_empty"`.
	inline := ThreadItem{
		Type: "imageGeneration",
		ID:   "call-inline",
		Data: map[string]any{"result": "aW5saW5l"},
	}
	inlinePayload := marshalThreadItemObject(t, &inline)
	if _, ok := inlinePayload["inputImages"]; ok {
		t.Fatalf("empty inputImages must be omitted: %#v", inlinePayload)
	}
}
