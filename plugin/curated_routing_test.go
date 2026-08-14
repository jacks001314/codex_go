package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTargetCuratedMarketplaceForRuntime(t *testing.T) {
	tests := []struct {
		name       string
		authMode   string
		providerID string
		want       TargetCuratedMarketplace
	}{
		{name: "no auth", want: TargetCuratedOpenAIAPI},
		{name: "chatgpt", authMode: "chatgpt", providerID: AmazonBedrockModelProviderID, want: TargetCuratedOpenAIWithRemote},
		{name: "external chatgpt", authMode: "chatgptAuthTokens", want: TargetCuratedOpenAIWithRemote},
		{name: "api key", authMode: "api-key", want: TargetCuratedOpenAIAPI},
		{name: "bedrock api key", authMode: "bedrock-api-key", providerID: AmazonBedrockModelProviderID, want: TargetCuratedOpenAIAPI},
		{name: "ambient bedrock", providerID: AmazonBedrockModelProviderID, want: TargetCuratedOpenAIAPI},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TargetCuratedMarketplaceForRuntime(test.authMode, test.providerID); got != test.want {
				t.Fatalf("TargetCuratedMarketplaceForRuntime(%q, %q) = %q, want %q", test.authMode, test.providerID, got, test.want)
			}
		})
	}
}

func TestPluginServiceSetRuntimeRouteReportsChange(t *testing.T) {
	service := NewPluginService()
	if !service.SetRuntimeRoute("chatgpt", "openai") {
		t.Fatal("first route set did not report a change")
	}
	if service.SetRuntimeRoute("chatgpt", "openai") {
		t.Fatal("same route unexpectedly reported a change")
	}
	if !service.SetRuntimeRoute("api-key", "openai") {
		t.Fatal("auth-mode route change did not report a change")
	}
}

func TestPluginServiceRoutesCuratedCapabilitiesHooksAndSkills(t *testing.T) {
	service := NewPluginService()
	for _, marketplace := range []string{OpenAICuratedMarketplaceName, OpenAIAPICuratedMarketplaceName, OpenAIRemoteCuratedMarketplaceName} {
		root := filepath.Join(t.TempDir(), marketplace)
		if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
		service.AddPlugin(PluginDetail{
			Summary: PluginSummary{
				ID:              "sample@" + marketplace,
				Name:            "sample",
				DisplayName:     marketplace,
				MarketplaceName: marketplace,
				Installed:       true,
				Enabled:         true,
				HasSkills:       true,
				Source:          PluginSource{Type: "local", Path: root},
			},
			ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json"),
			Hooks:        []PluginHookSummary{{Key: "hook"}},
		})
	}

	assertOnly := func(want string) {
		t.Helper()
		capabilities := service.EnabledCapabilities()
		if len(capabilities) != 1 || capabilities[0].ConfigName != "sample@"+want {
			t.Fatalf("capabilities = %#v, want %s", capabilities, want)
		}
		hooks := service.EnabledHookSources()
		if len(hooks) != 1 || hooks[0].PluginID != "sample@"+want {
			t.Fatalf("hooks = %#v, want %s", hooks, want)
		}
		roots := service.EnabledSkillRoots()
		if len(roots) != 1 || roots[0].PluginID != "sample@"+want {
			t.Fatalf("skill roots = %#v, want %s", roots, want)
		}
	}

	service.SetRuntimeRoute("", "openai")
	assertOnly(OpenAIAPICuratedMarketplaceName)
	service.SetRuntimeRoute("api-key", "openai")
	assertOnly(OpenAIAPICuratedMarketplaceName)
	service.SetRuntimeRoute("chatgpt", AmazonBedrockModelProviderID)
	assertOnly(OpenAIRemoteCuratedMarketplaceName)
}

func TestPluginServiceUsesExplicitAPICuratedManifest(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, ".tmp", "plugins")
	manifestRoot := filepath.Join(repo, ".agents", "plugins")
	pluginRoot := filepath.Join(repo, "plugins", "api-docs")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(manifestRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestRoot, "marketplace.json"), []byte(`{"name":"openai-curated","plugins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestRoot, "api_marketplace.json"), []byte(`{"name":"openai-api-curated","plugins":[{"name":"api-docs","source":{"source":"local","path":"./plugins/api-docs"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"api-docs"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetRuntimeRoute("api-key", "openai")
	response := service.List(&PluginListParams{})
	if len(response.Plugins) != 1 || response.Plugins[0].ID != "api-docs@openai-api-curated" {
		t.Fatalf("API curated plugins = %#v", response.Plugins)
	}
}

func TestEnabledMCPServerContributionsFollowCuratedRoute(t *testing.T) {
	service := NewPluginService()
	for _, marketplace := range []string{OpenAICuratedMarketplaceName, OpenAIAPICuratedMarketplaceName} {
		root := filepath.Join(t.TempDir(), marketplace)
		if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"type":"http","url":"https://example.test/mcp"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		service.AddPlugin(PluginDetail{Summary: PluginSummary{
			ID: "docs@" + marketplace, Name: "docs", MarketplaceName: marketplace,
			Installed: true, Enabled: true, Source: PluginSource{Type: "local", Path: root},
		}, ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json")})
	}
	service.SetRuntimeRoute("api-key", "openai")
	contributions := service.EnabledMCPServerContributions()
	if len(contributions) != 1 || contributions[0].PluginID != "docs@openai-api-curated" || contributions[0].Name != "docs" {
		t.Fatalf("contributions = %#v", contributions)
	}
}
