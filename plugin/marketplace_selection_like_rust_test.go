package plugin

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func writeMarketplaceSelectionFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #61686: repeated marketplace paths and shadowed plugin IDs across project
// scopes keep first-source precedence, and discarded rows never read their
// plugin manifests.
func TestConfiguredMarketplaceSelectionDefersDiscardedManifestReadsLikeRust(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "a-shared")
	repo := filepath.Join(root, "b-repo")
	writeMarketplaceSelectionFile(t, filepath.Join(shared, ".agents", "plugins", "marketplace.json"), `{
  "name": "team",
  "plugins": [
    {"name": "shared", "source": {"source": "local", "path": "./shared"}},
    {"name": "shadowed", "source": {"source": "local", "path": "./shadowed"}, "policy": {"products": []}}
  ]
}`)
	writeMarketplaceSelectionFile(t, filepath.Join(shared, "shared", ".codex-plugin", "plugin.json"), "{")
	writeMarketplaceSelectionFile(t, filepath.Join(repo, ".agents", "plugins", "marketplace.json"), `{
  "name": "team",
  "plugins": [
    {"name": "shadowed", "source": {"source": "local", "path": "./shadowed"}},
    {"name": "repo-only", "source": {"source": "local", "path": "./repo-only"}}
  ]
}`)
	writeMarketplaceSelectionFile(t, filepath.Join(repo, "shadowed", ".codex-plugin", "plugin.json"), "{")
	writeMarketplaceSelectionFile(t, filepath.Join(repo, "repo-only", ".codex-plugin", "plugin.json"), `{"name":"repo-only","version":"2.0.0"}`)

	var readsMu sync.Mutex
	var reads []string
	marketplacePluginManifestLoadHook = func(pluginRoot string) {
		readsMu.Lock()
		reads = append(reads, cleanPluginPathForCompare(pluginRoot))
		readsMu.Unlock()
	}
	t.Cleanup(func() { marketplacePluginManifestLoadHook = nil })

	service := NewPluginService()
	selection := NewMarketplaceSelection()
	sharedValues := map[string]any{"marketplaces": map[string]any{
		"team": map[string]any{"source_type": "local", "source": shared},
	}}
	// The second scope sees the same configured marketplace plus the repository
	// catalog, mirroring Rust's config roots followed by cwd discovery.
	repoValues := map[string]any{"marketplaces": map[string]any{
		"team": map[string]any{"source_type": "local", "source": shared},
		"repo": map[string]any{"source_type": "local", "source": repo},
	}}
	var entries []PluginMarketplaceEntry
	for _, values := range []map[string]any{sharedValues, repoValues} {
		resolved, loadErrors := service.ResolveConfigMarketplacesWithSelection(values, selection)
		if len(loadErrors) != 0 {
			t.Fatalf("load errors = %#v", loadErrors)
		}
		entries = append(entries, resolved...)
	}

	var ids []string
	var repoOnlyVersion string
	for _, entry := range entries {
		for _, summary := range entry.Plugins {
			ids = append(ids, summary.ID)
			if summary.ID == "repo-only@team" {
				repoOnlyVersion = stringPtrValue(summary.Version)
			}
		}
	}
	sort.Strings(ids)
	if want := []string{"repo-only@team", "shared@team"}; !equalStrings(ids, want) {
		t.Fatalf("plugin ids = %#v, want %#v", ids, want)
	}
	if repoOnlyVersion != "2.0.0" {
		t.Fatalf("repo-only version = %q", repoOnlyVersion)
	}

	readsMu.Lock()
	defer readsMu.Unlock()
	if len(reads) != 2 {
		t.Fatalf("manifest reads = %#v", reads)
	}
	for _, read := range reads {
		if strings.Contains(read, "shadowed") {
			t.Fatalf("discarded manifest was read: %#v", reads)
		}
	}
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestMarketplacePluginProductAllowedLikeRust(t *testing.T) {
	empty := []string{}
	codex := []string{"codex"}
	atlas := []string{"atlas"}
	cases := []struct {
		name       string
		products   *[]string
		restricted string
		want       bool
	}{
		{name: "unrestricted", products: nil, restricted: "codex", want: true},
		{name: "empty hides", products: &empty, restricted: "codex", want: false},
		{name: "matching product", products: &codex, restricted: "codex", want: true},
		{name: "other product", products: &atlas, restricted: "codex", want: false},
		{name: "no restriction product", products: &codex, restricted: "", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := marketplacePluginProductAllowed(testCase.products, testCase.restricted); got != testCase.want {
				t.Fatalf("allowed = %v, want %v", got, testCase.want)
			}
		})
	}
}
