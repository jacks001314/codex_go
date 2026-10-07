package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #49099 (core-plugins/src/manifest/manifest_cache_tests.rs): an unchanged
// revision reuses the parsed manifest, and callers never share one value.
func TestManifestCacheReusesParsedRevisionLikeRust(t *testing.T) {
	cache := newManifestCache()
	parses := 0
	parse := func() (*resolvedPluginManifest, error) {
		parses++
		return &resolvedPluginManifest{Manifest: pluginManifestFile{Name: "demo", Keywords: []string{"one"}}}, nil
	}
	first, err := cache.parse("/root/plugin", "/root/plugin/plugin.json", []byte("{}"), "", nil, parse)
	if err != nil {
		t.Fatalf("first parse error = %v", err)
	}
	second, err := cache.parse("/root/plugin", "/root/plugin/plugin.json", []byte("{}"), "", nil, parse)
	if err != nil {
		t.Fatalf("second parse error = %v", err)
	}
	if parses != 1 {
		t.Fatalf("parses = %d, want 1 (the revision is unchanged)", parses)
	}
	if first == second {
		t.Fatal("a cache hit must not hand back the cached value itself")
	}
	second.Manifest.Keywords[0] = "mutated"
	if got := first.Manifest.Keywords[0]; got != "one" {
		t.Fatalf("cached manifest was mutated through a hit: %q", got)
	}
}

// Rust #49099: a revised manifest contents invalidates the entry.
func TestManifestCacheInvalidatesOnRevisedContentsLikeRust(t *testing.T) {
	cache := newManifestCache()
	parses := 0
	parse := func() (*resolvedPluginManifest, error) {
		parses++
		return &resolvedPluginManifest{}, nil
	}
	if _, err := cache.parse("/root", "/root/plugin.json", []byte(`{"name":"one"}`), "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if _, err := cache.parse("/root", "/root/plugin.json", []byte(`{"name":"two"}`), "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parses != 2 {
		t.Fatalf("parses = %d, want 2 (the contents changed)", parses)
	}
}

// Rust #49099: the Codex overlay participates in the revision, so changing or
// removing it is visible.
func TestManifestCacheInvalidatesOnOverlayRevisionLikeRust(t *testing.T) {
	cache := newManifestCache()
	parses := 0
	parse := func() (*resolvedPluginManifest, error) {
		parses++
		return &resolvedPluginManifest{}, nil
	}
	overlayPath := "/root/.codex-plugin/plugin.json"
	if _, err := cache.parse("/root", "/root/plugin.json", []byte("{}"), overlayPath, []byte(`{"apps":[]}`), parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if _, err := cache.parse("/root", "/root/plugin.json", []byte("{}"), overlayPath, []byte(`{"apps":[]}`), parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parses != 1 {
		t.Fatalf("unchanged overlay parses = %d, want 1", parses)
	}
	if _, err := cache.parse("/root", "/root/plugin.json", []byte("{}"), overlayPath, []byte(`{"apps":[{"name":"x"}]}`), parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parses != 2 {
		t.Fatalf("revised overlay parses = %d, want 2", parses)
	}
	if _, err := cache.parse("/root", "/root/plugin.json", []byte("{}"), overlayPath, nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parses != 3 {
		t.Fatalf("removed overlay parses = %d, want 3 (removal is a revision)", parses)
	}
}

// Rust #49099: failed parses are never cached, so the next load repairs.
func TestManifestCacheDoesNotCacheFailuresLikeRust(t *testing.T) {
	cache := newManifestCache()
	parses := 0
	parse := func() (*resolvedPluginManifest, error) {
		parses++
		if parses == 1 {
			return nil, errors.New("invalid manifest")
		}
		return &resolvedPluginManifest{Manifest: pluginManifestFile{Name: "repaired"}}, nil
	}
	if _, err := cache.parse("/root", "/root/plugin.json", []byte("{}"), "", nil, parse); err == nil {
		t.Fatal("first parse error = nil, want the parse failure")
	}
	repaired, err := cache.parse("/root", "/root/plugin.json", []byte("{}"), "", nil, parse)
	if err != nil {
		t.Fatalf("second parse error = %v", err)
	}
	if parses != 2 || repaired == nil || repaired.Manifest.Name != "repaired" {
		t.Fatalf("parses = %d, manifest = %#v", parses, repaired)
	}
}

// Rust #49099: inputs over 64 KiB are never cached, and an existing entry is
// dropped so the oversized revision cannot be served.
func TestManifestCacheBypassesOversizedInputsLikeRust(t *testing.T) {
	cache := newManifestCache()
	parses := 0
	parse := func() (*resolvedPluginManifest, error) {
		parses++
		return &resolvedPluginManifest{}, nil
	}
	small := []byte("{}")
	if _, err := cache.parse("/root", "/root/plugin.json", small, "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	oversized := []byte(strings.Repeat("x", ManifestCacheMaxInputBytes+1))
	if _, err := cache.parse("/root", "/root/plugin.json", oversized, "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if _, err := cache.parse("/root", "/root/plugin.json", oversized, "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parses != 3 {
		t.Fatalf("parses = %d, want 3 (oversized revisions are never cached)", parses)
	}
	if got := cache.entries.Len(); got != 0 {
		t.Fatalf("cache entries = %d, want 0 (the small entry was dropped)", got)
	}
	// A manifest plus overlay over the bound is oversized too.
	if _, err := cache.parse("/root", "/root/plugin.json", small, "/overlay", []byte(strings.Repeat("y", ManifestCacheMaxInputBytes)), parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if got := cache.entries.Len(); got != 0 {
		t.Fatalf("cache entries = %d, want 0 (contents plus overlay over the bound)", got)
	}
}

// Rust #49099 (manifest_cache_tests.rs
// `equivalent_windows_paths_share_a_cached_revision`): case and separator
// spellings of one Windows path share a cache key.
func TestManifestCacheCollapsesEquivalentWindowsPathsLikeRust(t *testing.T) {
	cache := newManifestCache()
	parses := 0
	parse := func() (*resolvedPluginManifest, error) {
		parses++
		return &resolvedPluginManifest{}, nil
	}
	if _, err := cache.parse(`C:\Root\Plugin`, `C:\Root\Plugin\plugin.json`, []byte("{}"), "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if _, err := cache.parse(`c:/root/plugin`, `c:/root/plugin/plugin.json`, []byte("{}"), "", nil, parse); err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if parses != 1 {
		t.Fatalf("parses = %d, want 1 (both spellings are the same path identity)", parses)
	}
}

// Rust #49099: the cache is bounded to 128 revisions.
func TestManifestCacheIsBoundedLikeRust(t *testing.T) {
	cache := newManifestCache()
	parse := func() (*resolvedPluginManifest, error) { return &resolvedPluginManifest{}, nil }
	for i := 0; i < ManifestCacheCapacity+5; i++ {
		root := filepath.Join("/root", "plugin", string(rune('a'+i%26))+string(rune('a'+i/26)))
		if _, err := cache.parse(root, filepath.Join(root, "plugin.json"), []byte("{}"), "", nil, parse); err != nil {
			t.Fatalf("parse error = %v", err)
		}
	}
	if got := cache.entries.Len(); got > ManifestCacheCapacity {
		t.Fatalf("cache entries = %d, want at most %d", got, ManifestCacheCapacity)
	}
}

func writeLegacyManifest(t *testing.T, pluginRoot string, contents string) string {
	t.Helper()
	path := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write = %v", err)
	}
	return path
}

// Rust #49099: the store cache still rereads the file on every load, so a
// manifest edit is visible.
func TestPluginStoreManifestCacheObservesEditsLikeRust(t *testing.T) {
	pluginRoot := t.TempDir()
	writeLegacyManifest(t, pluginRoot, `{"name":"one"}`)
	store, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore error = %v", err)
	}
	first, err := store.LoadPluginManifest(pluginRoot)
	if err != nil || first == nil || first.Manifest.Name != "one" {
		t.Fatalf("first load = %#v, %v", first, err)
	}
	if _, err := store.LoadPluginManifest(pluginRoot); err != nil {
		t.Fatalf("second load error = %v", err)
	}
	writeLegacyManifest(t, pluginRoot, `{"name":"two"}`)
	revised, err := store.LoadPluginManifest(pluginRoot)
	if err != nil || revised == nil || revised.Manifest.Name != "two" {
		t.Fatalf("revised load = %#v, %v", revised, err)
	}
}

// Rust #49099: copies of one store share the cache; independently created
// stores do not.
func TestPluginStoreManifestCacheSharingLikeRust(t *testing.T) {
	pluginRoot := t.TempDir()
	writeLegacyManifest(t, pluginRoot, `{"name":"demo"}`)
	store, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore error = %v", err)
	}
	copyOfStore := *store
	if _, err := store.LoadPluginManifest(pluginRoot); err != nil {
		t.Fatalf("load error = %v", err)
	}
	if copyOfStore.manifests != store.manifests {
		t.Fatal("a store copy must share the manifest cache")
	}
	if _, err := copyOfStore.LoadPluginManifest(pluginRoot); err != nil {
		t.Fatalf("load error = %v", err)
	}
	if got := store.manifests.entries.Len(); got != 1 {
		t.Fatalf("shared cache entries = %d, want 1", got)
	}
	other, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore error = %v", err)
	}
	if other.manifests == store.manifests {
		t.Fatal("independent stores must not share the manifest cache")
	}
	if _, err := other.LoadPluginManifest(pluginRoot); err != nil {
		t.Fatalf("load error = %v", err)
	}
	if got := store.manifests.entries.Len(); got != 1 {
		t.Fatalf("first store entries = %d, want 1 (the other store is isolated)", got)
	}
}

// Rust #49099: an agent plugin's Codex overlay is part of the revision, so a
// removed overlay is visible to the next load.
func TestPluginStoreManifestCacheObservesOverlayRemovalLikeRust(t *testing.T) {
	pluginRoot := t.TempDir()
	agentManifest := `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"demo"}`
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(agentManifest), 0o644); err != nil {
		t.Fatalf("write = %v", err)
	}
	overlayPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(overlayPath), 0o755); err != nil {
		t.Fatalf("mkdir = %v", err)
	}
	if err := os.WriteFile(overlayPath, []byte(`{"apps":[{"name":"from-overlay"}]}`), 0o644); err != nil {
		t.Fatalf("write = %v", err)
	}
	store, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore error = %v", err)
	}
	withOverlay, err := store.LoadPluginManifest(pluginRoot)
	if err != nil || withOverlay == nil || len(withOverlay.Manifest.Apps) != 1 {
		t.Fatalf("with overlay = %#v, %v", withOverlay, err)
	}
	if err := os.Remove(overlayPath); err != nil {
		t.Fatalf("remove = %v", err)
	}
	withoutOverlay, err := store.LoadPluginManifest(pluginRoot)
	if err != nil || withoutOverlay == nil || len(withoutOverlay.Manifest.Apps) != 0 {
		t.Fatalf("without overlay = %#v, %v", withoutOverlay, err)
	}
}

// Rust #49099: the app-server materialization path reads manifests through the
// service's shared cache.
func TestPluginServiceManifestCacheIsWiredLikeRust(t *testing.T) {
	pluginRoot := t.TempDir()
	writeLegacyManifest(t, pluginRoot, `{"name":"demo"}`)
	service := NewPluginService()
	if service.manifests == nil {
		t.Fatal("PluginService must own a manifest cache")
	}
	if got := service.readPluginManifestForRootCached(pluginRoot); got == nil || got.Name != "demo" {
		t.Fatalf("read = %#v", got)
	}
	if _, err := parsePluginManifestAtRoot(pluginRoot, disabledManifestCache()); err != nil {
		t.Fatalf("uncached parse error = %v", err)
	}
	if got := service.manifests.entries.Len(); got != 1 {
		t.Fatalf("service cache entries = %d, want 1 (the materialization path is cached)", got)
	}
}
