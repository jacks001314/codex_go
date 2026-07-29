package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitAgentMarketplaceManifestResolvesSourcesFromRepoRoot(t *testing.T) {
	repo := t.TempDir()
	manifestPath := filepath.Join(repo, ".agents", "plugins", "api_marketplace.json")
	pluginRoot := filepath.Join(repo, "plugins", "api-docs")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"name":"openai-api-curated","plugins":[{"name":"api-docs","source":{"source":"local","path":"./plugins/api-docs"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"api-docs"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	details, errs := loadMarketplacePlugins([]Marketplace{{Name: OpenAIAPICuratedMarketplaceName, RootPath: manifestPath}})
	if len(errs) != 0 || len(details) != 1 || pluginRootFromManifestPath(details[0].ManifestPath) != pluginRoot {
		t.Fatalf("details = %#v, errors = %#v", details, errs)
	}
}
