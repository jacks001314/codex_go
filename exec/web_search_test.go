package exec

import (
	"testing"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/tool"
	"codex_go/turn"
)

func TestExecToolRouterRegistersConfiguredWebSearch(t *testing.T) {
	router, err := (&Runner{}).toolRouterForRequest(&Request{}, &agentRunConfig{
		WebSearch: &turn.WebSearchOptions{},
	})
	if err != nil {
		t.Fatalf("toolRouterForRequest() error = %v", err)
	}
	found := false
	for _, spec := range router.ModelVisibleSpecs() {
		if spec.Name == tool.NamespacedName(turn.WebSearchNamespace, turn.WebSearchRunTool) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("exec router did not expose web.run")
	}
}

func TestExecWebSearchModeHonorsConfigAndSearchFlag(t *testing.T) {
	tests := []struct {
		name      string
		values    map[string]any
		forceLive bool
		want      codexapi.WebSearchMode
	}{
		{name: "default", values: map[string]any{}, want: codexapi.WebSearchModeCached},
		{name: "legacy request", values: map[string]any{"features": map[string]any{"web_search_request": true}}, want: codexapi.WebSearchModeLive},
		{name: "legacy cached wins", values: map[string]any{"features": map[string]any{"web_search_cached": true, "web_search_request": true}}, want: codexapi.WebSearchModeCached},
		{name: "explicit indexed", values: map[string]any{"web_search": "indexed", "features": map[string]any{"web_search_request": true}}, want: codexapi.WebSearchModeIndexed},
		{name: "explicit disabled", values: map[string]any{"web_search": "disabled", "features": map[string]any{"web_search_request": true}}, want: codexapi.WebSearchModeDisabled},
		{name: "forced live", values: map[string]any{"web_search": "disabled"}, forceLive: true, want: codexapi.WebSearchModeLive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Values: tt.values}
			if got := execWebSearchMode(cfg, tt.forceLive); got != tt.want {
				t.Fatalf("execWebSearchMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
