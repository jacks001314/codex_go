package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #46108 replaced the persisted bundled-plugin exclusion state with a
// runtime guard: while the remote global catalog is active, a cached remote
// Sites install suppresses sites@openai-bundled even when it is disabled, and
// a missing remote bundle keeps the bundled fallback.
func TestSitesCompatibilityGuardInRuntimeLoadLikeRust(t *testing.T) {
	cases := []struct {
		name          string
		remoteEnabled bool
		cacheRemote   bool
		want          []string
	}{
		{"enabled remote supersedes bundled", true, true, []string{"sites@openai-curated-remote"}},
		{"disabled remote does not reactivate bundled", false, true, nil},
		{"missing remote bundle preserves bundled", true, false, []string{"sites@openai-bundled"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewPluginService()
			service.SetRuntimeRoute("chatgpt", "openai")
			addSitesRuntimePlugin(t, service, OpenAIBundledMarketplaceName, true)
			if tc.cacheRemote {
				addSitesRuntimePlugin(t, service, OpenAIRemoteCuratedMarketplaceName, tc.remoteEnabled)
			}
			assertSitesRuntimePlugins(t, service, tc.want)
		})
	}
}

// Without an active remote catalog the bundled Sites plugin is not superseded,
// matching the Rust guard being scoped to remote_global_catalog_active.
func TestSitesBundledFallbackStaysWithoutRemoteCatalogLikeRust(t *testing.T) {
	service := NewPluginService()
	service.SetRuntimeRoute("api-key", "openai")
	addSitesRuntimePlugin(t, service, OpenAIBundledMarketplaceName, true)
	addSitesRuntimePlugin(t, service, OpenAIRemoteCuratedMarketplaceName, true)
	assertSitesRuntimePlugins(t, service, []string{"sites@openai-bundled"})
}

func addSitesRuntimePlugin(t *testing.T, service *PluginService, marketplace string, enabled bool) {
	t.Helper()
	root := filepath.Join(t.TempDir(), marketplace)
	for _, dir := range []string{filepath.Join(root, "skills", "sites"), filepath.Join(root, "hooks")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create plugin directory: %v", err)
		}
	}
	service.AddPlugin(PluginDetail{
		Summary: PluginSummary{
			ID:              "sites@" + marketplace,
			Name:            "sites",
			DisplayName:     "Sites",
			MarketplaceName: marketplace,
			Installed:       true,
			Enabled:         enabled,
			HasSkills:       true,
			Source:          PluginSource{Type: "local", Path: root},
		},
		ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json"),
		Hooks:        []PluginHookSummary{{Key: "hook"}},
	})
}

func assertSitesRuntimePlugins(t *testing.T, service *PluginService, want []string) {
	t.Helper()
	surfaces := map[string][]string{
		"skills": idsOfSkillRoots(service.EnabledSkillRoots()),
		"hooks":  idsOfHookSources(service.EnabledHookSources()),
	}
	for surface, got := range surfaces {
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s plugins = %#v, want %#v", surface, got, want)
		}
	}
}

func idsOfSkillRoots(roots []EnabledSkillRoot) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, root.PluginID)
	}
	return out
}

func idsOfHookSources(sources []HookSource) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.PluginID)
	}
	return out
}
