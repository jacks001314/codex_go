package plugin

import "testing"

func bundledOrRemoteDetail(marketplace, name string) PluginDetail {
	return PluginDetail{Summary: PluginSummary{
		ID:              name + "@" + marketplace,
		Name:            name,
		MarketplaceName: marketplace,
	}}
}

// Rust #53747: a cached remote sites/visualize install supersedes the bundled
// plugin, while other bundled plugins and missing remote bundles keep the
// fallback.
func TestRemoteSupersedesBundledPluginsLikeRust(t *testing.T) {
	details := []PluginDetail{
		bundledOrRemoteDetail(OpenAIBundledMarketplaceName, "visualize"),
		bundledOrRemoteDetail(OpenAIRemoteCuratedMarketplaceName, "visualize"),
		bundledOrRemoteDetail(OpenAIBundledMarketplaceName, "sites"),
		bundledOrRemoteDetail(OpenAIRemoteCuratedMarketplaceName, "sites"),
		bundledOrRemoteDetail(OpenAIBundledMarketplaceName, "chrome"),
		bundledOrRemoteDetail(OpenAICuratedMarketplaceName, "calendar"),
		bundledOrRemoteDetail(OpenAIRemoteCuratedMarketplaceName, "calendar"),
	}
	routed := routePluginDetails(details, TargetCuratedOpenAIWithRemote)
	got := map[string]bool{}
	for _, detail := range routed {
		got[detail.Summary.ID] = true
	}
	for _, want := range []string{
		"visualize@openai-curated-remote",
		"sites@openai-curated-remote",
		"chrome@openai-bundled",
		"calendar@openai-curated-remote",
	} {
		if !got[want] {
			t.Fatalf("routed plugins missing %q: %#v", want, got)
		}
	}
	for _, absent := range []string{
		"visualize@openai-bundled",
		"sites@openai-bundled",
		"calendar@openai-curated",
	} {
		if got[absent] {
			t.Fatalf("routed plugins retained superseded %q: %#v", absent, got)
		}
	}
}

// A disabled remote entry still suppresses the bundled fallback; a missing
// remote entry preserves it.
func TestRuntimeEligibilityUsesRemoteBundledSupersedeLikeRust(t *testing.T) {
	service := &PluginService{
		targetCuratedMarketplace: TargetCuratedOpenAIWithRemote,
		plugins: map[string]PluginDetail{
			"visualize@openai-curated-remote": bundledOrRemoteDetail(OpenAIRemoteCuratedMarketplaceName, "visualize"),
		},
	}
	if service.pluginEligibleForRuntimeLocked(bundledOrRemoteDetail(OpenAIBundledMarketplaceName, "visualize")) {
		t.Fatal("a bundled visualize survived a cached remote install")
	}
	if !service.pluginEligibleForRuntimeLocked(bundledOrRemoteDetail(OpenAIBundledMarketplaceName, "sites")) {
		t.Fatal("a bundled sites plugin without a remote install was dropped")
	}
	if !service.pluginEligibleForRuntimeLocked(bundledOrRemoteDetail(OpenAIBundledMarketplaceName, "chrome")) {
		t.Fatal("an unrelated bundled plugin was dropped")
	}
}
