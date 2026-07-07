package model

import (
	"encoding/json"
	"testing"
)

func TestListModelsFiltersHiddenAndMarksDefault(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "hidden", DisplayName: "Hidden", Visibility: VisibilityNone, SupportedInAPI: true, Priority: 0},
		{Slug: "visible", DisplayName: "Visible", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 1, ServiceTiers: []string{"default"}, DefaultServiceTier: "default"},
	}})
	service := NewModelService(manager)
	visible, err := service.List(&ModelListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(visible.Models) != 1 || visible.Models[0].ID != "visible" || !visible.Models[0].IsDefault {
		t.Fatalf("visible = %#v", visible.Models)
	}
	includeHidden := true
	all, err := service.List(&ModelListParams{IncludeHidden: &includeHidden})
	if err != nil {
		t.Fatalf("List(include hidden) error = %v", err)
	}
	if len(all.Models) != 2 {
		t.Fatalf("all = %#v", all.Models)
	}
	encoded, err := json.Marshal(visible)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("UnmarshalJSON returned error: %v", err)
	}
	if _, ok := payload["models"]; ok {
		t.Fatalf("legacy models key should not be emitted: %#v", payload)
	}
	if _, ok := payload["data"]; !ok || payload["nextCursor"] != nil {
		t.Fatalf("payload = %#v", payload)
	}
	data := payload["data"].([]any)
	modelPayload := data[0].(map[string]any)
	for _, legacyKey := range []string{"name", "contextWindow", "supportsSearchTool"} {
		if _, ok := modelPayload[legacyKey]; ok {
			t.Fatalf("legacy model key %q should not be emitted: %#v", legacyKey, modelPayload)
		}
	}
}

func TestModelListParamsMarshalRustShape(t *testing.T) {
	payload := marshalObjectForTest(t, &ModelListParams{RefreshStrategy: string(RefreshOnline)})
	for _, nullableKey := range []string{"cursor", "limit", "includeHidden"} {
		if value, ok := payload[nullableKey]; !ok || value != nil {
			t.Fatalf("nullable key %q = %#v in %#v", nullableKey, value, payload)
		}
	}
	if _, ok := payload["refreshStrategy"]; ok {
		t.Fatalf("internal refreshStrategy should not be emitted: %#v", payload)
	}

	cursor := "2"
	limit := uint32(25)
	includeHidden := true
	payload = marshalObjectForTest(t, &ModelListParams{Cursor: &cursor, Limit: &limit, IncludeHidden: &includeHidden})
	if payload["cursor"] != "2" || payload["limit"].(float64) != 25 || payload["includeHidden"] != true {
		t.Fatalf("model list params = %#v", payload)
	}
}

func TestListModelsPaginationErrorsMatchRust(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "a", DisplayName: "A", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
		{Slug: "b", DisplayName: "B", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 1},
	}})
	service := NewModelService(manager)

	limit := uint32(0)
	first, err := service.List(&ModelListParams{Limit: &limit})
	if err != nil {
		t.Fatalf("List(limit=0) error = %v", err)
	}
	if len(first.Models) != 1 || first.NextCursor == nil || *first.NextCursor != "1" {
		t.Fatalf("first page = %#v next=%v", first.Models, first.NextCursor)
	}

	invalid := "invalid"
	if _, err := service.List(&ModelListParams{Cursor: &invalid}); err == nil || err.Error() != "invalid cursor: invalid" {
		t.Fatalf("invalid cursor error = %v", err)
	}

	beyond := "3"
	if _, err := service.List(&ModelListParams{Cursor: &beyond}); err == nil || err.Error() != "cursor 3 exceeds total models 2" {
		t.Fatalf("beyond cursor error = %v", err)
	}
}

func marshalObjectForTest(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return payload
}

func TestProviderCapabilities(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
		Slug:                       "gpt-test",
		Visibility:                 VisibilityVisible,
		SupportedInAPI:             true,
		Priority:                   0,
		SupportedReasoningLevels:   []string{"low", "high"},
		SupportsReasoningSummaries: true,
		SupportsParallelToolCalls:  true,
		InputModalities:            []string{"text", "image"},
	}}})
	response := NewModelService(manager).ProviderCapabilities(&ProviderCapabilitiesReadParams{})
	if !response.NamespaceTools || !response.ImageGeneration || response.WebSearch {
		t.Fatalf("response = %#v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("UnmarshalJSON returned error: %v", err)
	}
	if len(payload) != 3 || payload["namespaceTools"] != true || payload["imageGeneration"] != true || payload["webSearch"] != false {
		t.Fatalf("payload = %#v", payload)
	}
}
