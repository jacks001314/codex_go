package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

func TestWebSearchRuntimeModeControlsExternalAccess(t *testing.T) {
	for _, test := range []struct {
		mode        string
		wantMode    codexapi.WebSearchMode
		wantBoolean *bool
		wantIndexed bool
	}{
		{mode: "disabled", wantMode: codexapi.WebSearchModeDisabled, wantBoolean: boolPtrAppWebSearch(false)},
		{mode: "cached", wantMode: codexapi.WebSearchModeCached, wantBoolean: boolPtrAppWebSearch(false)},
		{mode: "indexed", wantMode: codexapi.WebSearchModeIndexed, wantIndexed: true},
		{mode: "live", wantMode: codexapi.WebSearchModeLive, wantBoolean: boolPtrAppWebSearch(true)},
	} {
		cfg := &config.Config{Values: map[string]any{"web_search": test.mode}}
		if got := webSearchModeFromConfig(cfg); got != test.wantMode {
			t.Fatalf("mode %q resolved to %q", test.mode, got)
		}
		access := webSearchSettingsFromConfig(cfg, test.wantMode).ExternalWebAccess
		if test.wantIndexed {
			if access.Mode == nil || *access.Mode != codexapi.ExternalWebIndexed {
				t.Fatalf("indexed access = %#v", access)
			}
		} else if access.Boolean == nil || test.wantBoolean == nil || *access.Boolean != *test.wantBoolean {
			t.Fatalf("mode %q access = %#v", test.mode, access)
		}
	}
}

func TestWebSearchRuntimeModeResolvesLegacyFeaturesLikeRust(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]any
		want   codexapi.WebSearchMode
	}{
		{name: "default", values: map[string]any{}, want: codexapi.WebSearchModeCached},
		{
			name:   "legacy request enables live",
			values: map[string]any{"features": map[string]any{"web_search_request": true}},
			want:   codexapi.WebSearchModeLive,
		},
		{
			name: "legacy cached wins over legacy request",
			values: map[string]any{"features": map[string]any{
				"web_search_cached":  true,
				"web_search_request": true,
			}},
			want: codexapi.WebSearchModeCached,
		},
		{
			name: "explicit mode wins over legacy features",
			values: map[string]any{
				"web_search": "disabled",
				"features":   map[string]any{"web_search_request": true},
			},
			want: codexapi.WebSearchModeDisabled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{Values: test.values}
			if got := webSearchModeFromConfig(cfg); got != test.want {
				t.Fatalf("webSearchModeFromConfig() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWebSearchRuntimeDisabledModeDoesNotExposeStandaloneTool(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"web_search": "disabled",
		"features":   map[string]any{"standalone_web_search": true},
	}}
	options, err := (&RuntimeRouter{}).webSearchOptionsForTurn(cfg, &turn.TurnStartParams{})
	if err != nil {
		t.Fatalf("webSearchOptionsForTurn() error = %v", err)
	}
	if options != nil {
		t.Fatalf("disabled mode options = %#v", options)
	}
}

func TestAppStandaloneWebSearchEnabledMatchesRustPlanning(t *testing.T) {
	capabilities := model.ProviderCapabilities{NamespaceTools: true, WebSearch: true}
	for _, test := range []struct {
		name     string
		caps     model.ProviderCapabilities
		info     *model.ModelInfo
		features map[string]bool
		want     bool
	}{
		{name: "responses lite without feature", caps: capabilities, info: &model.ModelInfo{UseResponsesLite: true}, want: true},
		{name: "standard with feature", caps: capabilities, info: &model.ModelInfo{}, features: map[string]bool{"standalone_web_search": true}, want: true},
		{name: "standard without feature", caps: capabilities, info: &model.ModelInfo{}, want: false},
		{name: "missing namespace tools", caps: model.ProviderCapabilities{WebSearch: true}, info: &model.ModelInfo{UseResponsesLite: true}, want: false},
		{name: "missing web search capability", caps: model.ProviderCapabilities{NamespaceTools: true}, info: &model.ModelInfo{UseResponsesLite: true}, want: false},
		{name: "missing model info", caps: capabilities, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := appStandaloneWebSearchEnabled(test.caps, test.info, test.features); got != test.want {
				t.Fatalf("appStandaloneWebSearchEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWebSearchResultsPersistAndMarshalOpaqueFields(t *testing.T) {
	now := time.Now().UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "web-call",
			ToolName: tool.NamespacedName(turn.WebSearchNamespace, turn.WebSearchRunTool),
		},
		Output: &tool.Output{
			CallID:   "web-call",
			ToolName: tool.NamespacedName(turn.WebSearchNamespace, turn.WebSearchRunTool),
			Success:  true,
			Data: map[string]any{
				"web_search_action":  map[string]any{"type": "search", "query": "codex"},
				"web_search_results": []any{map[string]any{"type": "future", "new_field": map[string]any{"x": 1}}},
			},
		},
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
	}
	item, ok := sessionItemForWebSearchExecution("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForWebSearchExecution() did not recognize web.run")
	}
	results, ok := item.Data["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("persisted results = %#v", item.Data["results"])
	}
	threadItem := BuildThreadItem(item)
	data, err := json.Marshal(&threadItem)
	if err != nil {
		t.Fatalf("Marshal(thread item) error = %v", err)
	}
	for _, want := range []string{`"type":"webSearch"`, `"results":[`, `"new_field":{"x":1}`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("thread item missing %s: %s", want, data)
		}
	}
}

func boolPtrAppWebSearch(value bool) *bool { return &value }
