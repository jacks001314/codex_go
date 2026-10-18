package plugin

import "testing"

func TestCanonicalAppIDsForDisabledPluginsMatchIdentityAndMarketplaceLikeRust(t *testing.T) {
	service := NewPluginService()
	canonical := "calendar"
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:              "calendar@openai-curated-remote",
			Name:            "calendar",
			MarketplaceName: "openai-curated-remote",
			Installed:       true,
			Enabled:         true,
		},
		CanonicalAppID:           &canonical,
		CanonicalAppAuthIdentity: "account-123",
	})
	got := service.CanonicalAppIDsForDisabledPlugins([]string{"calendar@openai-curated-remote"}, "account-123")
	if _, ok := got["calendar"]; !ok || len(got) != 1 {
		t.Fatalf("canonical connector exclusions = %#v", got)
	}
	if got := service.CanonicalAppIDsForDisabledPlugins([]string{"calendar@openai-curated-remote"}, "account-other"); len(got) != 0 {
		t.Fatalf("stale-auth exclusions = %#v, want none", got)
	}
	if got := service.CanonicalAppIDsForDisabledPlugins([]string{"calendar@created-by-me-remote"}, "account-123"); len(got) != 0 {
		t.Fatalf("other-marketplace exclusions = %#v, want none", got)
	}
	if got := service.CanonicalAppIDsForDisabledPlugins([]string{"another@openai-curated-remote"}, "account-123"); len(got) != 0 {
		t.Fatalf("unrelated plugin exclusions = %#v, want none", got)
	}
}
