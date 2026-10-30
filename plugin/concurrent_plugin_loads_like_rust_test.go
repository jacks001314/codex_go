package plugin

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors Rust #57819: plugin loads overlap up to eight at a time and results
// keep configured order.
func TestConcurrentPluginLoadsBoundAndOrderLikeRust(t *testing.T) {
	const count = maxConcurrentPluginLoads + 4
	var active atomic.Int64
	var maxActive atomic.Int64
	results := loadPluginsConcurrently(count, func(index int) int {
		current := active.Add(1)
		for {
			seen := maxActive.Load()
			if current <= seen || maxActive.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return index * 10
	})
	if len(results) != count {
		t.Fatalf("results = %d, want %d", len(results), count)
	}
	for index, result := range results {
		if result != index*10 {
			t.Fatalf("result[%d] = %d, want %d (order must be preserved)", index, result, index*10)
		}
	}
	if got := maxActive.Load(); got > maxConcurrentPluginLoads {
		t.Fatalf("max concurrent loads = %d, want <= %d", got, maxConcurrentPluginLoads)
	}
	if maxActive.Load() < 2 {
		t.Fatalf("loads did not overlap (max = %d)", maxActive.Load())
	}
}

// Mirrors Rust #57819: a marketplace manifest loads its plugins concurrently but
// reports details in manifest order, which decides duplicate MCP precedence.
func TestMarketplaceManifestKeepsPluginOrderLikeRust(t *testing.T) {
	root := t.TempDir()
	const manifest = `{
  "name": "test",
  "plugins": [
    {"name": "plugin-02"},
    {"name": "plugin-00"},
    {"name": "plugin-01"}
  ]
}`
	manifestPath := filepath.Join(root, "marketplace.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	details, err := loadMarketplaceManifest(Marketplace{Name: "test", RootPath: root}, manifestPath, disabledManifestCache())
	if err != nil {
		t.Fatalf("load marketplace manifest: %v", err)
	}
	want := []string{"plugin-02", "plugin-00", "plugin-01"}
	if len(details) != len(want) {
		t.Fatalf("details = %#v", details)
	}
	for index, name := range want {
		if details[index].Summary.Name != name {
			t.Fatalf("details[%d] = %q, want %q", index, details[index].Summary.Name, name)
		}
	}
}
