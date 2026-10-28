package protocol

import (
	"encoding/json"
	"testing"
)

func TestImageGenerationItemCarriesGenerationIDLikeRust(t *testing.T) {
	generationID := "gen-image-123"
	item := ImageGenerationItemWithID("img-1", "completed", "a red cube", "", nil, &generationID)
	if item.GenerationID == nil || *item.GenerationID != generationID {
		t.Fatalf("GenerationID = %#v", item.GenerationID)
	}
	if item.Metadata["generationId"] != generationID || item.Metadata["generation_id"] != generationID {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["generation_id"] != generationID {
		t.Fatalf("generation_id = %#v", decoded["generation_id"])
	}

	without := ImageGenerationItem("img-2", "completed", "a red cube", "")
	if without.GenerationID != nil {
		t.Fatalf("GenerationID = %#v, want nil", without.GenerationID)
	}
	if _, ok := without.Metadata["generationId"]; ok {
		t.Fatalf("metadata unexpectedly contains generationId: %#v", without.Metadata)
	}
}
