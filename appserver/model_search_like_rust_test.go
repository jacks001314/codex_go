package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func modelSearchTestConfig(values map[string]any) *config.Config {
	if values == nil {
		values = map[string]any{}
	}
	values["features"] = map[string]any{
		"apps":                  true,
		"standalone_web_search": true,
		"hosted_search_policy":  false,
	}
	return &config.Config{Values: values}
}

func modelSearchTestTool() mcp.RuntimeToolInfo {
	return mcp.RuntimeToolInfo{
		ServerName:    mcp.RuntimeCodexAppsMCPServerName,
		HostOwnedApps: true,
		ConnectorID:   mcp.SearchConnector,
		Tool:          mcp.RuntimeTool{Name: "search_service.web_run"},
	}
}

func modelSearchTestModel(provider string) *model.ModelInfo {
	info := &model.ModelInfo{ExperimentalSupportedTools: []string{"literature_search_query"}}
	if provider != "" {
		info.WebSearchProvider = &provider
	}
	return info
}

func readySearchDiscovery(_ context.Context, _ map[string]any) mcp.SearchDiscovery {
	return mcp.SearchDiscovery{Kind: mcp.SearchDiscoveryReady, Binding: &mcp.Binding{}}
}

func TestResolveModelSearchProviderPrecedenceLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		info     *model.ModelInfo
		discover modelSearchDiscoveryFunc
		want     modelSearchProvider
		mode     codexapi.WebSearchMode
	}{
		{name: "model native", cfg: modelSearchTestConfig(map[string]any{"web_search": "live"}), info: modelSearchTestModel("standalone"), want: modelSearchNative, mode: codexapi.WebSearchModeLive},
		{name: "model hosted", cfg: modelSearchTestConfig(map[string]any{"web_search": "live"}), info: modelSearchTestModel("search_mcp"), discover: readySearchDiscovery, want: modelSearchHosted},
		{name: "client mcp", cfg: modelSearchTestConfig(map[string]any{"web_search": "live", "tools": map[string]any{"client_search_provider": "search_mcp"}}), info: &model.ModelInfo{}, want: modelSearchClientMCP},
		{name: "client native", cfg: modelSearchTestConfig(map[string]any{"web_search": "cached", "tools": map[string]any{"client_search_provider": "standalone"}}), info: &model.ModelInfo{}, want: modelSearchNative, mode: codexapi.WebSearchModeCached},
		{name: "unconfigured", cfg: modelSearchTestConfig(map[string]any{"web_search": "live"}), info: &model.ModelInfo{}, want: modelSearchUnmanaged, mode: codexapi.WebSearchModeLive},
		{name: "managed native", cfg: &config.Config{Values: map[string]any{"web_search": "live", "features": map[string]any{"apps": true, "standalone_web_search": true}}, Requirements: &config.ConfigRequirements{FeatureRequirements: map[string]bool{"standalone_web_search": true}}}, info: modelSearchTestModel("search_mcp"), want: modelSearchNative, mode: codexapi.WebSearchModeLive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveModelSearchSelection(context.Background(), modelSearchResolveInput{
				cfg:             test.cfg,
				modelInfo:       test.info,
				tools:           []mcp.RuntimeToolInfo{modelSearchTestTool()},
				nativeAvailable: true,
				turnMetadata:    map[string]any{"mcp_request_meta": map[string]any{}},
			}, test.discover)
			if got.provider != test.want || got.mode != test.mode {
				t.Fatalf("selection = %#v, want provider %q mode %q", got, test.want, test.mode)
			}
		})
	}
}

func TestResolveModelSearchPolicyAndFallbackLikeRust(t *testing.T) {
	cfg := modelSearchTestConfig(map[string]any{"web_search": "live", "tools": map[string]any{"web_search": map[string]any{"allowed_domains": []any{"example.com"}}}})
	hosted := modelSearchTestModel("search_mcp")
	tests := []struct {
		name         string
		request      map[string]any
		owner        modelSearchProvider
		usedMCP      bool
		attribution  bool
		kind         mcp.SearchDiscoveryKind
		want         modelSearchProvider
		wantFallback bool
		wantDisabled bool
	}{
		{name: "critical falls back", request: map[string]any{}, kind: mcp.SearchDiscoveryCritical, want: modelSearchNative, wantFallback: true},
		{name: "unavailable does not fall back", request: map[string]any{}, kind: mcp.SearchDiscoveryUnavailable, wantDisabled: true},
		{name: "hosted use blocks fallback", request: map[string]any{}, kind: mcp.SearchDiscoveryCritical, usedMCP: true, wantDisabled: true},
		{name: "MCP ownership blocks fallback", request: map[string]any{}, kind: mcp.SearchDiscoveryCritical, owner: modelSearchClientMCP, wantDisabled: true},
		{name: "attribution blocks fallback", request: map[string]any{}, kind: mcp.SearchDiscoveryCritical, attribution: true, wantDisabled: true},
		{name: "host request does not authorize native fallback", request: map[string]any{"request_id": "request-1"}, kind: mcp.SearchDiscoveryCritical, wantDisabled: true},
		{name: "domain mismatch blocks hosted", request: map[string]any{"openai/search_context": map[string]any{"allowed_domains": []any{"other.example"}}}, kind: mcp.SearchDiscoveryReady, wantDisabled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			discover := func(context.Context, map[string]any) mcp.SearchDiscovery {
				return mcp.SearchDiscovery{Kind: test.kind, Binding: &mcp.Binding{}}
			}
			got := resolveModelSearchSelection(context.Background(), modelSearchResolveInput{
				cfg: cfg, modelInfo: hosted, tools: []mcp.RuntimeToolInfo{modelSearchTestTool()}, nativeAvailable: true,
				owner: test.owner, usedMCP: test.usedMCP, attributionBlocks: test.attribution,
				turnMetadata: map[string]any{"mcp_request_meta": test.request},
			}, discover)
			if test.wantDisabled {
				if got.provider != modelSearchDisabled {
					t.Fatalf("selection = %#v, want disabled", got)
				}
				return
			}
			if got.provider != test.want || got.fallback != test.wantFallback {
				t.Fatalf("selection = %#v, want provider %q fallback %v", got, test.want, test.wantFallback)
			}
		})
	}
}

func TestResolveModelSearchDiscoveryCarriesScopedContextLikeRust(t *testing.T) {
	cfg := modelSearchTestConfig(map[string]any{
		"web_search": "live",
		"tools":      map[string]any{"web_search": map[string]any{"allowed_domains": []any{"example.com"}}},
	})
	var metadata map[string]any
	got := resolveModelSearchSelection(context.Background(), modelSearchResolveInput{
		cfg:             cfg,
		modelInfo:       modelSearchTestModel("search_mcp"),
		tools:           []mcp.RuntimeToolInfo{modelSearchTestTool()},
		nativeAvailable: true,
		turnMetadata:    map[string]any{"mcp_request_meta": map[string]any{"openai/search_context": map[string]any{"mode": "cached"}}},
	}, func(_ context.Context, value map[string]any) mcp.SearchDiscovery {
		metadata = value
		return mcp.SearchDiscovery{Kind: mcp.SearchDiscoveryReady, Binding: &mcp.Binding{}}
	})
	if got.provider != modelSearchHosted {
		t.Fatalf("selection = %#v, want hosted", got)
	}
	request := metadata["mcp_request_meta"].(map[string]any)
	if connectors := request["selected_connector_ids"].([]any); len(connectors) != 1 || connectors[0] != mcp.SearchConnector {
		t.Fatalf("discovery request = %#v", request)
	}
	contextValue := request["openai/search_context"].(map[string]any)
	if contextValue["mode"] != "cached" || contextValue["is_web_access_restricted"] != true {
		t.Fatalf("search context = %#v", contextValue)
	}
	if tools := contextValue["model_supported_tools"].([]any); len(tools) != 1 || tools[0] != "literature_search_query" {
		t.Fatalf("model_supported_tools = %#v", contextValue["model_supported_tools"])
	}
	if domains := contextValue["allowed_domains"].([]any); len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("allowed_domains = %#v", contextValue["allowed_domains"])
	}
}

func TestSearchCallTurnMetadataReplacesOnlySearchContextLikeRust(t *testing.T) {
	source := map[string]any{
		"thread_id":        "thread-1",
		"mcp_request_meta": `{"request_id":"request-1","openai/search_context":{"mode":"live"}}`,
	}
	got := searchCallTurnMetadata(func() map[string]any { return source }, map[string]any{"mode": "cached", "is_web_access_restricted": true})()
	if got["thread_id"] != "thread-1" {
		t.Fatalf("turn metadata = %#v", got)
	}
	request := got["mcp_request_meta"].(map[string]any)
	if request["request_id"] != "request-1" {
		t.Fatalf("unrelated request metadata was lost: %#v", request)
	}
	contextValue := request["openai/search_context"].(map[string]any)
	if contextValue["mode"] != "cached" || contextValue["is_web_access_restricted"] != true {
		t.Fatalf("search context = %#v", contextValue)
	}
	// The callback must not mutate the turn's shared metadata document.
	if source["mcp_request_meta"] != `{"request_id":"request-1","openai/search_context":{"mode":"live"}}` {
		t.Fatalf("base metadata mutated: %#v", source)
	}
}

func TestApplyModelSearchToolsRemovesConflictingProviderLikeRust(t *testing.T) {
	base := []mcp.RuntimeToolInfo{modelSearchTestTool(), {ServerName: "local", Tool: mcp.RuntimeTool{Name: "read"}}}
	for _, test := range []struct {
		provider modelSearchProvider
		want     int
	}{
		{provider: modelSearchNative, want: 1},
		{provider: modelSearchDisabled, want: 1},
		{provider: modelSearchUnmanaged, want: 2},
		{provider: modelSearchClientMCP, want: 2},
		{provider: modelSearchHosted, want: 1},
	} {
		selection := modelSearchSelection{provider: test.provider}
		if test.provider == modelSearchHosted {
			selection.binding = &mcp.Binding{}
		}
		if got := applyModelSearchTools(base, selection); len(got) != test.want {
			t.Fatalf("provider %q tools = %#v, want %d", test.provider, got, test.want)
		}
	}
}

func TestModelSearchOwnershipAndPersistenceLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if router.claimModelSearchProvider("thread-1", modelSearchClientMCP) != true {
		t.Fatal("first MCP reservation failed")
	}
	if router.claimModelSearchProvider("thread-1", modelSearchNative) {
		t.Fatal("native reservation replaced an active MCP owner")
	}
	router.recordModelSearchUse("thread-1", "turn-1", modelSearchClientMCP)
	if !router.modelSearchTurnUses("thread-1", "turn-1", modelSearchUsedMCP) {
		t.Fatal("MCP turn usage was not recorded")
	}

	if !router.claimModelSearchProvider("thread-2", modelSearchNative) {
		t.Fatal("native reservation failed")
	}
	items := []session.Item{{ID: "search-1", Type: "webSearch", Data: map[string]any{"query": "paper"}}}
	router.annotateModelSearchMetadata("thread-2", items)
	raw, _ := items[0].Data["harness_metadata"].(json.RawMessage)
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields["native_search_fallback_used"] != true {
		t.Fatalf("harness metadata = %#v", items[0].Data["harness_metadata"])
	}
	if !historyUsesNativeSearch(items) {
		t.Fatal("native ownership was not recoverable from history")
	}
}

func TestModelSearchFallbackReservationReleasesUnusedOfferLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	id, release, ok := router.reserveModelSearchNative("thread-1")
	if !ok || id == 0 || release == nil {
		t.Fatal("native fallback reservation failed")
	}
	if got := router.threadModelSearchProvider("thread-1"); got != modelSearchNative {
		t.Fatalf("reserved provider = %q, want native", got)
	}
	if router.claimModelSearchProvider("thread-1", modelSearchClientMCP) {
		t.Fatal("MCP claimed a thread with an active native offer")
	}
	release()
	if got := router.threadModelSearchProvider("thread-1"); got != "" {
		t.Fatalf("unused fallback left provider %q", got)
	}
	if !router.claimModelSearchProvider("thread-1", modelSearchClientMCP) {
		t.Fatal("MCP could not claim after the unused offer was released")
	}
}

func TestModelSearchFallbackReservationBecomesDurableWhenUsedLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	_, release, ok := router.reserveModelSearchNative("thread-2")
	if !ok {
		t.Fatal("native fallback reservation failed")
	}
	if !router.claimModelSearchProvider("thread-2", modelSearchNative) {
		t.Fatal("native search could not use its fallback reservation")
	}
	release()
	if got := router.threadModelSearchProvider("thread-2"); got != modelSearchNative {
		t.Fatalf("used fallback provider = %q, want durable native ownership", got)
	}
	if router.claimModelSearchProvider("thread-2", modelSearchClientMCP) {
		t.Fatal("MCP replaced a used native fallback")
	}
}

func TestNativeModelSearchOptionsRejectsMCPOwnedThreadLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	options := router.nativeModelSearchOptions(&turn.WebSearchOptions{}, "thread-1", "turn-1")
	if options == nil || options.Admit == nil {
		t.Fatal("native search options have no admission gate")
	}
	if err := options.Admit(); err != nil {
		t.Fatalf("first native admission = %v", err)
	}
	if !router.claimModelSearchProvider("thread-2", modelSearchClientMCP) {
		t.Fatal("MCP reservation failed")
	}
	if err := router.nativeModelSearchOptions(&turn.WebSearchOptions{}, "thread-2", "turn-1").Admit(); err == nil {
		t.Fatal("native admission succeeded on an MCP-owned thread")
	}
}
