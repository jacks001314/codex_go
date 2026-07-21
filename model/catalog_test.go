package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestStaticModelsManagerListsSortedModelsAndMarksDefault(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "slow", DisplayName: "slow", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 20},
		{Slug: "fast", DisplayName: "fast", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 5},
		{Slug: "hidden", DisplayName: "hidden", Visibility: VisibilityNone, SupportedInAPI: true, Priority: 0},
	}})

	models := manager.ListModels(RefreshOffline)
	if len(models) != 2 {
		t.Fatalf("models len = %d", len(models))
	}
	if models[0].Model != "fast" || !models[0].IsDefault {
		t.Fatalf("first model = %#v", models[0])
	}
	if models[1].Model != "slow" || models[1].IsDefault {
		t.Fatalf("second model = %#v", models[1])
	}
}

func TestBuildAvailableModelsMarksFirstPickerVisibleDefault(t *testing.T) {
	models := BuildAvailableModels([]ModelInfo{
		{Slug: "hidden", DisplayName: "Hidden", Visibility: VisibilityHide, SupportedInAPI: true, Priority: 0},
		{Slug: "listed", DisplayName: "Listed", Visibility: VisibilityList, SupportedInAPI: true, Priority: 1},
	})
	if len(models) != 2 {
		t.Fatalf("models len = %d", len(models))
	}
	if models[0].IsDefault || !models[1].IsDefault {
		t.Fatalf("defaults = %#v", models)
	}
}

func TestBuildAvailableModelsPreservesReasoningFields(t *testing.T) {
	models := BuildAvailableModels([]ModelInfo{
		{
			Slug:                     "gpt-reasoning",
			DisplayName:              "GPT Reasoning",
			Visibility:               VisibilityList,
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium", "high"},
		},
	})
	if len(models) != 1 {
		t.Fatalf("models len = %d", len(models))
	}
	got := models[0]
	if got.DefaultReasoningLevel != "medium" {
		t.Fatalf("default reasoning = %q", got.DefaultReasoningLevel)
	}
	if len(got.SupportedReasoningLevels) != 3 || got.SupportedReasoningLevels[2] != "high" {
		t.Fatalf("supported reasoning = %#v", got.SupportedReasoningLevels)
	}
}

func TestDefaultBaseInstructionsRequirePreambleBeforeTools(t *testing.T) {
	for _, want := range []string{"Before making tool calls", "brief preamble", "immediately about to happen", "Progress updates"} {
		if !strings.Contains(BaseInstructions, want) {
			t.Fatalf("BaseInstructions missing %q", want)
		}
	}
}

func TestModelInfoUnmarshalRustCatalogShape(t *testing.T) {
	var catalog ModelsResponse
	if err := json.Unmarshal([]byte(`{
		"models": [{
			"slug": "gpt-test",
			"display_name": "GPT Test",
			"description": null,
			"default_reasoning_level": "medium",
			"supported_reasoning_levels": [{"effort": "low", "description": "Low"}],
			"visibility": "list",
			"supported_in_api": true,
			"priority": 1,
			"service_tiers": [{"id": "priority", "name": "Fast", "description": "Fast tier"}],
			"additional_speed_tiers": ["fast"],
			"default_service_tier": "priority",
			"base_instructions": "base",
			"model_messages": {
				"instructions_template": "Hello {{ personality }}",
				"instructions_variables": {
					"personality_default": "Default",
					"personality_friendly": "Friendly",
					"personality_pragmatic": "Pragmatic"
				}
			},
			"truncation_policy": {"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": true,
			"context_window": 272000,
			"max_context_window": 1000000,
			"effective_context_window_percent": 95,
			"input_modalities": ["text", "image"],
			"supports_search_tool": true
		}]
	}`), &catalog); err != nil {
		t.Fatalf("Unmarshal catalog returned error: %v", err)
	}
	model := catalog.Models[0]
	if model.Description != "" || model.SupportedReasoningLevels[0] != "low" || model.ServiceTiers[0] != "priority" {
		t.Fatalf("model = %#v", model)
	}
	if model.Visibility != VisibilityList || !model.SupportsParallelToolCalls || !model.SupportsSearchTool {
		t.Fatalf("model flags = %#v", model)
	}
	if model.ModelMessages == nil || model.ModelMessages.PersonalityFriendly != "Friendly" {
		t.Fatalf("model messages = %#v", model.ModelMessages)
	}
}

func TestServiceTierForRequest(t *testing.T) {
	info := &ModelInfo{ServiceTiers: []string{"priority", "flex"}}
	if got := ServiceTierForRequest(info, "fast"); got != "priority" {
		t.Fatalf("fast tier = %q", got)
	}
	if got := ServiceTierForRequest(info, "default"); got != "" {
		t.Fatalf("default tier = %q", got)
	}
	if got := ServiceTierForRequest(info, "turbo"); got != "" {
		t.Fatalf("unsupported tier = %q", got)
	}
	if got := ServiceTierForRequest(&ModelInfo{UsedFallbackModelMetadata: true}, "priority"); got != "" {
		t.Fatalf("fallback model tier = %q", got)
	}
}

func TestRemoteModelsManagerRefreshesOnlineAndETag(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{
			{
				Models: []ModelInfo{{
					Slug:           "remote",
					DisplayName:    "Remote",
					Visibility:     VisibilityVisible,
					SupportedInAPI: true,
					Priority:       0,
				}},
				ETag: "etag-1",
			},
			{
				Models: []ModelInfo{{
					Slug:           "remote",
					DisplayName:    "Remote Updated",
					Visibility:     VisibilityVisible,
					SupportedInAPI: true,
					Priority:       0,
				}},
				ETag: "etag-2",
			},
		},
	}
	manager := NewRemoteModelsManager(&ModelsResponse{Models: []ModelInfo{{
		Slug:           "bundled",
		DisplayName:    "Bundled",
		Visibility:     VisibilityVisible,
		SupportedInAPI: true,
		Priority:       10,
	}}}, endpoint)

	offline := manager.ListModels(RefreshOffline)
	if len(offline) != 1 || offline[0].Model != "bundled" || endpoint.calls != 0 {
		t.Fatalf("offline models = %#v, calls = %d", offline, endpoint.calls)
	}
	online := manager.ListModels(RefreshOnlineIfUncached)
	if endpoint.calls != 1 {
		t.Fatalf("calls after online_if_uncached = %d", endpoint.calls)
	}
	if len(online) != 2 || online[0].Model != "remote" || online[1].Model != "bundled" {
		t.Fatalf("online models = %#v", online)
	}
	_ = manager.ListModels(RefreshOnlineIfUncached)
	if endpoint.calls != 1 {
		t.Fatalf("calls after cached online_if_uncached = %d", endpoint.calls)
	}
	manager.RefreshIfNewETag("etag-1")
	if endpoint.calls != 1 {
		t.Fatalf("calls after same etag = %d", endpoint.calls)
	}
	manager.RefreshIfNewETag("etag-2")
	if endpoint.calls != 2 || endpoint.etags[1] != "etag-1" {
		t.Fatalf("calls = %d etags = %#v", endpoint.calls, endpoint.etags)
	}
	updated := manager.GetModelInfo("remote", nil)
	if updated.DisplayName != "Remote Updated" {
		t.Fatalf("updated model = %#v", updated)
	}
}

func TestRemoteModelsManagerCanUseRemoteCatalogAsSourceOfTruth(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "chatgpt-remote",
				DisplayName:    "ChatGPT Remote",
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

	models := manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 1 || models[0].Model != "chatgpt-remote" {
		t.Fatalf("models = %#v", models)
	}
}

func TestRemoteModelsManagerKeepsMergingForAPIAuthAndHiddenOnlyRemote(t *testing.T) {
	endpoint := &recordingModelsEndpoint{
		responses: []*ModelsEndpointResponse{{
			Models: []ModelInfo{{
				Slug:           "hidden-remote",
				DisplayName:    "Hidden Remote",
				Visibility:     VisibilityHide,
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

	models := manager.ListModels(RefreshOnlineIfUncached)
	if len(models) != 2 || models[0].Model != "hidden-remote" || models[1].Model != "bundled" {
		t.Fatalf("models = %#v", models)
	}
}

func TestHTTPModelsEndpointSendsHeadersAndParsesModels(t *testing.T) {
	var gotPath string
	var gotIfNoneMatch string
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set(modelsEndpointETagHeader, "etag-remote")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"slug":"remote","display_name":"Remote","visibility":"visible","supported_in_api":true,"priority":0}]}`))
	}))
	defer server.Close()

	endpoint := NewHTTPModelsEndpoint(
		&APIProvider{BaseURL: server.URL + "/v1", QueryParams: map[string]string{"api-version": "1"}},
		&AuthHeaders{Headers: http.Header{"Authorization": []string{"Bearer token"}}},
		server.Client(),
	)
	response, err := endpoint.ListModels(nil, "etag-local")
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	parsedPath, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("request path parse error: %v", err)
	}
	query := parsedPath.Query()
	if parsedPath.Path != "/v1/models" || query.Get("api-version") != "1" || query.Get("client_version") != "0.0.0" || gotIfNoneMatch != "etag-local" || gotAuthorization != "Bearer token" {
		t.Fatalf("request path=%q if-none-match=%q authorization=%q", gotPath, gotIfNoneMatch, gotAuthorization)
	}
	if response.ETag != "etag-remote" || len(response.Models) != 1 || response.Models[0].Slug != "remote" {
		t.Fatalf("response = %#v", response)
	}
}

func TestHTTPModelsEndpointAlwaysSendsClientVersion(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	endpoint := NewHTTPModelsEndpoint(&APIProvider{BaseURL: server.URL + "/v1"}, nil, server.Client())
	if _, err := endpoint.ListModels(nil, ""); err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	parsedPath, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("request path parse error: %v", err)
	}
	if parsedPath.Path != "/v1/models" || parsedPath.Query().Get("client_version") != "0.0.0" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestHTTPModelsEndpointAppliesRequestSigner(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	endpoint := NewHTTPModelsEndpoint(
		&APIProvider{BaseURL: server.URL + "/v1"},
		&AuthHeaders{
			SignRequest: func(_ context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
				request.Header.Set("Authorization", "Signed models")
				return &SignedRequest{Body: body}, nil
			},
		},
		server.Client(),
	)
	if _, err := endpoint.ListModels(nil, ""); err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if gotAuthorization != "Signed models" {
		t.Fatalf("Authorization = %q", gotAuthorization)
	}
}

func TestStaticModelsManagerDefaultModelPolicy(t *testing.T) {
	manager := NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{
		{Slug: "default-model", DisplayName: "default-model", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
		{Slug: "requested-model", DisplayName: "requested-model", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 10},
	}})

	if got := manager.GetDefaultModel("", false, RefreshOffline); got != "default-model" {
		t.Fatalf("default model = %q", got)
	}
	if got := manager.GetDefaultModel("missing-model", false, RefreshOffline); got != "missing-model" {
		t.Fatalf("preserved model = %q", got)
	}
	if got := manager.GetDefaultModel("missing-model", true, RefreshOffline); got != "default-model" {
		t.Fatalf("fallback model = %q", got)
	}
	if got := manager.GetDefaultModel("requested-model", true, RefreshOffline); got != "requested-model" {
		t.Fatalf("available requested model = %q", got)
	}
}

func TestConstructModelInfoUsesLongestPrefix(t *testing.T) {
	candidates := []ModelInfo{
		{Slug: "gpt-5", DisplayName: "gpt-5", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 10},
		{Slug: "gpt-5.3-codex", DisplayName: "codex", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
	}
	info := ConstructModelInfoFromCandidates("gpt-5.3-codex-special", candidates, nil)
	if info.DisplayName != "codex" {
		t.Fatalf("DisplayName = %q", info.DisplayName)
	}
	if info.Slug != "gpt-5.3-codex-special" {
		t.Fatalf("Slug = %q", info.Slug)
	}
	if info.UsedFallbackModelMetadata {
		t.Fatal("UsedFallbackModelMetadata = true, want false")
	}
}

func TestConstructModelInfoUsesNamespacedSuffix(t *testing.T) {
	candidates := []ModelInfo{
		{Slug: "gpt-5.3-codex", DisplayName: "codex", Visibility: VisibilityVisible, SupportedInAPI: true, Priority: 0},
	}
	info := ConstructModelInfoFromCandidates("custom/gpt-5.3-codex", candidates, nil)
	if info.DisplayName != "codex" {
		t.Fatalf("DisplayName = %q", info.DisplayName)
	}
	if info.Slug != "custom/gpt-5.3-codex" {
		t.Fatalf("Slug = %q", info.Slug)
	}
}

func TestConstructModelInfoFallsBackForUnknownModel(t *testing.T) {
	info := ConstructModelInfoFromCandidates("unknown-model", nil, nil)
	if !info.UsedFallbackModelMetadata {
		t.Fatal("UsedFallbackModelMetadata = false, want true")
	}
	if info.DisplayName != "unknown-model" {
		t.Fatalf("DisplayName = %q", info.DisplayName)
	}
}

func TestWithConfigOverrides(t *testing.T) {
	supportsReasoningSummaries := true
	model := ModelInfoFromSlug("unknown-model")
	model.MaxContextWindow = 400000
	model.TruncationPolicy = TruncationPolicy{Mode: TruncationModeTokens, Limit: 10}

	updated := WithConfigOverrides(model, &ModelsManagerConfig{
		ModelContextWindow:              500000,
		ModelAutoCompactTokenLimit:      12345,
		ToolOutputTokenLimit:            456,
		BaseInstructions:                "custom instructions",
		PersonalityEnabled:              true,
		ModelSupportsReasoningSummaries: &supportsReasoningSummaries,
	})

	if !updated.SupportsReasoningSummaries {
		t.Fatal("SupportsReasoningSummaries = false, want true")
	}
	if updated.ContextWindow != 400000 {
		t.Fatalf("ContextWindow = %d", updated.ContextWindow)
	}
	if updated.AutoCompactTokenLimit != 12345 {
		t.Fatalf("AutoCompactTokenLimit = %d", updated.AutoCompactTokenLimit)
	}
	if updated.TruncationPolicy.Mode != TruncationModeTokens || updated.TruncationPolicy.Limit != 456 {
		t.Fatalf("TruncationPolicy = %#v", updated.TruncationPolicy)
	}
	if updated.BaseInstructions != "custom instructions" {
		t.Fatalf("BaseInstructions = %q", updated.BaseInstructions)
	}
	if updated.ModelMessages != nil {
		t.Fatal("ModelMessages should be cleared when base instructions override is present")
	}
}

func TestPersonalityDisabledClearsModelMessages(t *testing.T) {
	model := ModelInfoFromSlug("gpt-5.2-codex")
	if model.ModelMessages == nil {
		t.Fatal("ModelMessages is nil before override")
	}
	updated := WithConfigOverrides(model, &ModelsManagerConfig{PersonalityEnabled: false})
	if updated.ModelMessages != nil {
		t.Fatal("ModelMessages should be cleared")
	}
}

func TestModelInstructionsPersonalityTemplate(t *testing.T) {
	info := ModelInfo{
		BaseInstructions: "base",
		ModelMessages: &ModelMessages{
			InstructionsTemplate: "Hello {{ personality }}",
			PersonalityDefault:   "default",
			PersonalityFriendly:  "friendly",
			PersonalityPragmatic: "pragmatic",
		},
	}
	if got := info.ModelInstructions("friendly"); got != "Hello friendly" {
		t.Fatalf("friendly instructions = %q", got)
	}
	if got := info.ModelInstructions("none"); got != "Hello " {
		t.Fatalf("none instructions = %q", got)
	}
	if !info.SupportsPersonality() {
		t.Fatal("SupportsPersonality = false")
	}

	info.ModelMessages.InstructionsTemplate = ""
	if got := info.ModelInstructions("friendly"); got != "base" {
		t.Fatalf("missing template instructions = %q", got)
	}
}

func TestAmazonBedrockModelCatalog(t *testing.T) {
	manager := NewStaticModelsManager(AmazonBedrockModelCatalog())
	models := manager.ListModels(RefreshOffline)
	want := []string{
		AmazonBedrockGPT55ModelID,
		AmazonBedrockGPT54ModelID,
		AmazonBedrockGPT56SolModelID,
		AmazonBedrockGPT56TerraModelID,
		AmazonBedrockGPT56LunaModelID,
	}
	if len(models) != len(want) {
		t.Fatalf("models len = %d", len(models))
	}
	for i, wantModel := range want {
		if models[i].Model != wantModel {
			t.Fatalf("model[%d] = %q, want %q", i, models[i].Model, wantModel)
		}
	}
	if !models[0].IsDefault {
		t.Fatal("first Bedrock model should be default")
	}
}

func TestWithDefaultOnlyServiceTierClearsTiers(t *testing.T) {
	catalog := ModelsResponse{Models: []ModelInfo{{
		Slug:                 "gpt-5.5",
		AdditionalSpeedTiers: []string{"fast"},
		ServiceTiers:         []string{"default", "priority"},
		DefaultServiceTier:   "default",
	}}}
	updated := WithDefaultOnlyServiceTier(catalog)
	model := updated.Models[0]
	if len(model.AdditionalSpeedTiers) != 0 || len(model.ServiceTiers) != 0 || model.DefaultServiceTier != "" {
		t.Fatalf("model tiers = %#v", model)
	}
}

type recordingModelsEndpoint struct {
	calls     int
	etags     []string
	responses []*ModelsEndpointResponse
}

func (e *recordingModelsEndpoint) ListModels(_ context.Context, etag string) (*ModelsEndpointResponse, error) {
	e.calls++
	e.etags = append(e.etags, etag)
	if len(e.responses) == 0 {
		return &ModelsEndpointResponse{}, nil
	}
	response := e.responses[0]
	e.responses = e.responses[1:]
	return response, nil
}
