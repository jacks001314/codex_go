package codexapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Rust #62871 (codex-api/src/images.rs `deserialize_image_metadata`): each
// `data[]` entry carries its own optional `image_metadata` object. Empty,
// non-object, oversized, or deeply nested values are discarded without
// dropping the image they belong to.
func TestImageResponsePreservesOptionalMetadataOnEachImageLikeRust(t *testing.T) {
	oversized := strings.Repeat("x", 1024*1024)
	body := `{"created":1,"data":[
		{"b64_json":"first","generation_id":"gen-first","image_metadata":{"entries":[{"name":"first","value":1.5}]}},
		{"b64_json":"second","generation_id":"gen-second","image_metadata":{"futureKey":{"zero":0,"disabled":false,"nestedKeys":[null,"x"]}}},
		{"b64_json":"legacy"},
		{"b64_json":"empty","image_metadata":{}},
		{"b64_json":"null","image_metadata":null},
		{"b64_json":"list","image_metadata":[]},
		{"b64_json":"scalar","image_metadata":0},
		{"b64_json":"oversized","image_metadata":{"value":"` + oversized + `"}}
	]}`
	var response ImageResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(response.Data) != 8 {
		t.Fatalf("len(data) = %d, want 8", len(response.Data))
	}
	if got := response.Data[0].GenerationID; got == nil || *got != "gen-first" {
		t.Fatalf("data[0].generation_id = %#v", got)
	}
	first := marshalMetadataForTest(t, response.Data[0].ImageMetadata)
	if first != `{"entries":[{"name":"first","value":1.5}]}` {
		t.Fatalf("data[0].image_metadata = %s", first)
	}
	future, ok := response.Data[1].ImageMetadata["futureKey"].(map[string]any)
	if !ok {
		t.Fatalf("data[1].futureKey = %#v", response.Data[1].ImageMetadata["futureKey"])
	}
	nested, ok := future["nestedKeys"].([]any)
	if !ok || len(nested) != 2 || nested[0] != nil || nested[1] != "x" {
		t.Fatalf("data[1].futureKey.nestedKeys = %#v", future["nestedKeys"])
	}
	for index, label := range []string{"legacy", "empty", "null", "list", "scalar", "oversized"} {
		image := response.Data[index+2]
		if image.B64JSON != label {
			t.Fatalf("data[%d].b64_json = %q, want %q", index+2, image.B64JSON, label)
		}
		if len(image.ImageMetadata) != 0 {
			t.Fatalf("data[%d].image_metadata = %#v, want empty", index+2, image.ImageMetadata)
		}
	}
}

// The field-level decode keeps the surrounding image alive even when the
// metadata itself cannot be represented within the 24-level limit.
func TestImageResponseDeepMetadataDoesNotDropImagesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		depth int
		keep  bool
	}{
		{23, true},
		{24, false},
		{120, false},
		{512, false},
	} {
		t.Run(fmt.Sprintf("depth-%d", tc.depth), func(t *testing.T) {
			nested := strings.Repeat(`{"child":`, tc.depth) + "null" + strings.Repeat("}", tc.depth)
			body := fmt.Sprintf(`{"created":1,"data":[
				{"image_metadata":{"nested":%s},"b64_json":"first","generation_id":"gen-first"},
				{"b64_json":"second","image_metadata":{"value":1}}
			]}`, nested)
			var response ImageResponse
			if err := json.Unmarshal([]byte(body), &response); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if len(response.Data) != 2 {
				t.Fatalf("len(data) = %d, want 2", len(response.Data))
			}
			if response.Data[0].B64JSON != "first" {
				t.Fatalf("data[0].b64_json = %q", response.Data[0].B64JSON)
			}
			if got := response.Data[0].GenerationID; got == nil || *got != "gen-first" {
				t.Fatalf("data[0].generation_id = %#v", got)
			}
			_, kept := response.Data[0].ImageMetadata["nested"]
			if kept != tc.keep {
				t.Fatalf("nested metadata kept = %v, want %v", kept, tc.keep)
			}
			if response.Data[1].B64JSON != "second" {
				t.Fatalf("data[1].b64_json = %q", response.Data[1].B64JSON)
			}
			if got := response.Data[1].ImageMetadata["value"]; got != json.Number("1") {
				t.Fatalf("data[1].value = %#v, want 1", got)
			}
		})
	}
}

// Persisted history uses serde_json's default recursion limit instead of the
// tighter API limit, and non-object values never reach the item.
func TestStoredImageMetadataLimitsLikeRust(t *testing.T) {
	if got := ParseStoredImageMetadataJSON([]byte(`[]`)); got != nil {
		t.Fatalf("array metadata = %#v, want nil", got)
	}
	if got := ParseStoredImageMetadataJSON([]byte(`null`)); got != nil {
		t.Fatalf("null metadata = %#v, want nil", got)
	}
	if got := ParseStoredImageMetadataJSON([]byte(`{}`)); got != nil {
		t.Fatalf("empty metadata = %#v, want nil", got)
	}
	nested := strings.Repeat(`{"child":`, 40) + "null" + strings.Repeat("}", 40)
	got := ParseStoredImageMetadataJSON([]byte(`{"nested":` + nested + `}`))
	if _, ok := got["nested"]; !ok {
		t.Fatalf("40-level stored metadata was dropped: %#v", got)
	}
	tooDeep := strings.Repeat(`{"child":`, 200) + "null" + strings.Repeat("}", 200)
	if got := ParseStoredImageMetadataJSON([]byte(`{"nested":` + tooDeep + `}`)); got != nil {
		t.Fatalf("200-level stored metadata = %#v, want nil", got)
	}
}

func marshalMetadataForTest(t *testing.T, metadata map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal(metadata) error = %v", err)
	}
	return string(encoded)
}
