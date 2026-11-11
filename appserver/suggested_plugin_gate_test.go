package appserver

import (
	"context"
	"sync/atomic"
	"testing"

	"codex_go/config"
	"codex_go/plugin"
)

type countingSuggestedPluginProvider struct {
	calls atomic.Int32
}

func (p *countingSuggestedPluginProvider) ListSuggestedPlugins(context.Context) (*plugin.SuggestedPluginList, error) {
	p.calls.Add(1)
	return &plugin.SuggestedPluginList{Enabled: true}, nil
}

// Mirrors Rust #60721: plugin recommendation cache warming is gated by
// Features::plugin_recommendations_enabled. Go installs the provider lazily, so
// the same gate clears it and no fetch can start.
func TestSuggestedPluginWarmHonorsRecommendationFeatureGateLikeRust(t *testing.T) {
	plugins := plugin.NewPluginService()
	provider := &countingSuggestedPluginProvider{}
	plugins.SetSuggestedPluginProviderWithKey(provider, "endpoint")
	router := NewRuntimeRouter(RuntimeServices{
		Plugins: plugins,
		Config:  config.NewConfigService(t.TempDir()),
	})
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{
		"apps":                true,
		"plugins":             true,
		"tool_suggest":        false,
		"recommended_plugins": false,
	}}}
	router.configureSuggestedPluginProviderForTurn(cfg)
	plugins.PrewarmSuggestedPlugins(context.Background())
	if got := provider.calls.Load(); got != 0 {
		t.Fatalf("recommendation fetches = %d, want 0 while the feature is disabled", got)
	}
}
