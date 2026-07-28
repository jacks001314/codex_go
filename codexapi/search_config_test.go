package codexapi

import (
	"encoding/json"
	"testing"
)

func TestWebSearchModeAndSettingsMatchRust(t *testing.T) {
	tests := []struct {
		value       any
		wantMode    WebSearchMode
		wantBoolean *bool
		wantModeVal *ExternalWebAccessMode
	}{
		{value: nil, wantMode: WebSearchModeCached, wantBoolean: boolPtrSearch(false)},
		{value: "disabled", wantMode: WebSearchModeDisabled, wantBoolean: boolPtrSearch(false)},
		{value: "cached", wantMode: WebSearchModeCached, wantBoolean: boolPtrSearch(false)},
		{value: "indexed", wantMode: WebSearchModeIndexed, wantModeVal: externalModePtr(ExternalWebIndexed)},
		{value: "live", wantMode: WebSearchModeLive, wantBoolean: boolPtrSearch(true)},
		{value: true, wantMode: WebSearchModeLive, wantBoolean: boolPtrSearch(true)},
	}
	for _, test := range tests {
		mode := WebSearchModeFromValue(test.value)
		if mode != test.wantMode {
			t.Fatalf("WebSearchModeFromValue(%#v) = %q, want %q", test.value, mode, test.wantMode)
		}
		access := SearchSettingsForMode(mode, nil).ExternalWebAccess
		if !equalBoolPtr(access.Boolean, test.wantBoolean) || !equalExternalModePtr(access.Mode, test.wantModeVal) {
			t.Fatalf("mode %q external access = %#v", mode, access)
		}
	}
}

func TestSearchSettingsForModePreservesToolConfig(t *testing.T) {
	settings := SearchSettingsForMode(WebSearchModeLive, map[string]any{
		"context_size":    "high",
		"allowed_domains": []any{"example.com", "openai.com"},
		"location": map[string]any{
			"country":  "US",
			"timezone": "America/Los_Angeles",
		},
	})
	if settings.SearchContextSize == nil || *settings.SearchContextSize != SearchContextHigh {
		t.Fatalf("context size = %#v", settings.SearchContextSize)
	}
	if settings.Filters == nil || len(settings.Filters.AllowedDomains) != 2 {
		t.Fatalf("filters = %#v", settings.Filters)
	}
	if settings.UserLocation == nil || settings.UserLocation.Country == nil || *settings.UserLocation.Country != "US" {
		t.Fatalf("location = %#v", settings.UserLocation)
	}
}

func TestSearchResponseKeepsOpaqueResults(t *testing.T) {
	var response SearchResponse
	if err := json.Unmarshal([]byte(`{"output":"ok","results":[{"type":"future","new_field":{"x":1}}]}`), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %#v", response.Results)
	}
	result, ok := response.Results[0].(map[string]any)
	if !ok || result["type"] != "future" {
		t.Fatalf("result = %#v", response.Results[0])
	}
}

func boolPtrSearch(value bool) *bool { return &value }

func externalModePtr(value ExternalWebAccessMode) *ExternalWebAccessMode { return &value }

func equalBoolPtr(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalExternalModePtr(left, right *ExternalWebAccessMode) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
