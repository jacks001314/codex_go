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

func TestListModelsExposesMultiAgentVersion(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "v2", DisplayName: "V2", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0, MultiAgentVersion: "v2"},
		{Slug: "disabled", DisplayName: "Disabled", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 1, MultiAgentVersion: "disabled"},
	}})
	response, err := NewModelService(manager).List(&ModelListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	byID := make(map[string]ModelSummary, len(response.Data))
	for _, summary := range response.Data {
		byID[summary.ID] = summary
	}
	if got := byID["v2"].MultiAgentVersion; got == nil || *got != "v2" {
		t.Fatalf("v2 multi-agent version = %#v", got)
	}
	if got := byID["disabled"].MultiAgentVersion; got == nil || *got != "disabled" {
		t.Fatalf("disabled multi-agent version = %#v", got)
	}
	payload := marshalObjectForTest(t, response)
	data := payload["data"].([]any)
	for _, raw := range data {
		model := raw.(map[string]any)
		if model["id"] == "v2" && model["multiAgentVersion"] != "v2" {
			t.Fatalf("wire model = %#v", model)
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

func TestListModelsDefaultsToOnlineIfUncachedLikeRust(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "chatgpt-remote-only",
				DisplayName:    "ChatGPT Remote Only",
				Description:    "Remote model",
				Visibility:     VisibilityList,
				SupportedInAPI: true,
				Priority:       0,
			}},
		}},
	}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{{
			Slug:           "bundled",
			DisplayName:    "Bundled",
			Visibility:     VisibilityVisible,
			SupportedInAPI: true,
			Priority:       10,
		}}},
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: true,
	})
	service := NewModelService(manager)

	first, err := service.List(&ModelListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("endpoint calls = %d, want 1", endpoint.calls)
	}
	if len(first.Models) != 1 || first.Models[0].ID != "chatgpt-remote-only" || !first.Models[0].IsDefault {
		t.Fatalf("first models = %#v", first.Models)
	}

	second, err := service.List(&ModelListParams{})
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("endpoint calls after cached list = %d, want 1", endpoint.calls)
	}
	if len(second.Models) != 1 || second.Models[0].ID != "chatgpt-remote-only" {
		t.Fatalf("second models = %#v", second.Models)
	}
}

func TestListModelsExplicitOfflineDoesNotRefresh(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "remote",
				DisplayName:    "Remote",
				Visibility:     VisibilityList,
				SupportedInAPI: true,
				Priority:       0,
			}},
		}},
	}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{{
			Slug:           "bundled",
			DisplayName:    "Bundled",
			Visibility:     VisibilityVisible,
			SupportedInAPI: true,
			Priority:       10,
		}}},
		Endpoint: endpoint,
	})
	service := NewModelService(manager)

	offline, err := service.List(&ModelListParams{RefreshStrategy: string(RefreshOffline)})
	if err != nil {
		t.Fatalf("List(offline) error = %v", err)
	}
	if endpoint.calls != 0 {
		t.Fatalf("endpoint calls = %d, want 0", endpoint.calls)
	}
	if len(offline.Models) != 1 || offline.Models[0].ID != "bundled" {
		t.Fatalf("offline models = %#v", offline.Models)
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
