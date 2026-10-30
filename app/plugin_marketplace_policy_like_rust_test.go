package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/plugin"
)

func TestPluginCLIContextAppliesMarketplacePolicyLikeRust(t *testing.T) {
	blockedHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(blockedHome, "requirements.toml"), []byte(`
[marketplaces]
restrict_to_allowed_sources = true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := newPluginCLIContext(blockedHome)
	if _, err := blocked.service.AddMarketplace(&plugin.MarketplaceAddParams{URL: "https://github.com/example/blocked.git"}); err == nil || !strings.Contains(err.Error(), "is not allowed by requirements") {
		t.Fatalf("blocked AddMarketplace() error = %v", err)
	}

	allowedHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(allowedHome, "requirements.toml"), []byte(`
[marketplaces]
restrict_to_allowed_sources = true
[marketplaces.allowed_sources.company]
source = "git"
url = "https://github.com/example/plugins.git"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	allowed := newPluginCLIContext(allowedHome)
	allowed.service.SetMarketplaceMaterializer(plugin.MarketplaceMaterializerFunc(func(*plugin.ParsedMarketplaceSource, []string, string) error {
		return nil
	}))
	if _, err := allowed.service.AddMarketplace(&plugin.MarketplaceAddParams{URL: "https://github.com/example/plugins.git"}); err != nil {
		t.Fatalf("allowlisted AddMarketplace() error = %v", err)
	}
}
