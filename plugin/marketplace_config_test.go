package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigMarketplacesFromValues(t *testing.T) {
	svc := NewPluginService()
	marketRoot := t.TempDir()
	manifest := `{"name":"acme","plugins":[{"name":"acme-tool","source":{"source":"local","path":"./plugins/acme-tool"}}]}`
	manifestPath := filepath.Join(marketRoot, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{
		"marketplaces": map[string]any{
			"acme": map[string]any{"source_type": "local", "source": marketRoot},
		},
	}
	entries, loadErrors := svc.ResolveConfigMarketplaces(values)
	if len(loadErrors) != 0 {
		t.Fatalf("load errors = %#v", loadErrors)
	}
	if len(entries) != 1 || entries[0].Name != "acme" {
		t.Fatalf("config marketplaces = %#v", entries)
	}
	if len(entries[0].Plugins) != 1 || entries[0].Plugins[0].Name != "acme-tool" {
		t.Fatalf("config marketplace plugins = %#v", entries[0].Plugins)
	}
}

func TestResolveConfigMarketplacesEmptyConfig(t *testing.T) {
	svc := NewPluginService()
	if entries, loadErrors := svc.ResolveConfigMarketplaces(nil); entries != nil || loadErrors != nil {
		t.Fatalf("nil config should resolve empty: entries=%#v errors=%#v", entries, loadErrors)
	}
	if entries, loadErrors := svc.ResolveConfigMarketplaces(map[string]any{}); entries != nil || loadErrors != nil {
		t.Fatalf("empty config should resolve empty: entries=%#v errors=%#v", entries, loadErrors)
	}
	// A config that declares no marketplaces resolves empty too.
	if entries, _ := svc.ResolveConfigMarketplaces(map[string]any{"features": map[string]any{"plugins": true}}); entries != nil {
		t.Fatalf("no-marketplaces config should resolve empty: %#v", entries)
	}
}
