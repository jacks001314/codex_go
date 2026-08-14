package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteInlineVisualizationsStreamingFallbackAndCodeFence(t *testing.T) {
	partial := "before\n::codex-inline-vis{file=\"chart"
	cleaned, items := RewriteInlineVisualizations(partial, nil)
	if cleaned != "before\n" || len(items) != 0 {
		t.Fatalf("partial rewrite = %q %#v", cleaned, items)
	}
	code := "```text\n::codex-inline-vis{file=\"chart.html\"}\n```"
	cleaned, _ = RewriteInlineVisualizations(code, nil)
	if cleaned != code {
		t.Fatalf("code directive changed = %q", cleaned)
	}
}

func TestRewriteInlineVisualizationsMaterializesSandboxedViewer(t *testing.T) {
	home := t.TempDir()
	threadDir := filepath.Join(home, "visualizations", "2026", "07", "21", "thread")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "chart.html"), []byte(`<canvas id="chart"></canvas><script>globalThis.ready=true</script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	context := &InlineVisualizationContext{VisualizationsDir: filepath.Join(home, "visualizations"), ThreadDir: threadDir}
	cleaned, items := RewriteInlineVisualizations("before\n::codex-inline-vis{file=\"chart.html\"}\nafter", context)
	if len(items) != 1 || !strings.Contains(cleaned, "Open chart visualization in the browser") || !strings.Contains(cleaned, items[0].URL) {
		t.Fatalf("rewrite = %q %#v", cleaned, items)
	}
	viewerURL := strings.TrimPrefix(items[0].URL, "file://")
	viewer, err := os.ReadFile(filepath.FromSlash(viewerURL))
	if err != nil {
		t.Fatal(err)
	}
	document := string(viewer)
	if !strings.Contains(document, `sandbox="allow-scripts"`) || strings.Contains(document, "allow-same-origin") || !strings.Contains(document, "globalThis.ready=true") {
		t.Fatalf("viewer contract = %q", document)
	}
}

func TestRewriteInlineVisualizationsRejectsUnsafeArtifacts(t *testing.T) {
	for _, directive := range []string{
		`::codex-inline-vis{file="../chart.html"}`,
		`::codex-inline-vis{file="chart.svg"}`,
		`::codex-inline-vis{file="missing.html"}`,
	} {
		cleaned, items := RewriteInlineVisualizations(directive, &InlineVisualizationContext{})
		if cleaned != "_Visualization unavailable on this device._" || len(items) != 0 {
			t.Fatalf("unsafe rewrite = %q %#v", cleaned, items)
		}
	}
}

func TestRewriteInlineVisualizationsMaterializesViewerInDedicatedCache(t *testing.T) {
	home := t.TempDir()
	threadDir := filepath.Join(home, "visualizations", "2026", "07", "21", "thread")
	viewerDir := filepath.Join(home, "visualization-viewers", "thread")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "chart.html"), []byte(`<canvas id="chart"></canvas><script>globalThis.ready=true</script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	context := &InlineVisualizationContext{
		VisualizationsDir: filepath.Join(home, "visualizations"),
		ThreadDir:         threadDir,
		ViewerDir:         viewerDir,
	}
	_, items := RewriteInlineVisualizations("::codex-inline-vis{file=\"chart.html\"}", context)
	if len(items) != 1 {
		t.Fatalf("rewrite items = %#v", items)
	}
	viewerURL := strings.TrimPrefix(items[0].URL, "file://")
	if !strings.HasPrefix(filepath.Clean(viewerURL), filepath.Clean(viewerDir)) {
		t.Fatalf("viewer URL %q not under dedicated cache %q", viewerURL, viewerDir)
	}
	if _, err := os.Stat(filepath.FromSlash(viewerURL)); err != nil {
		t.Fatalf("viewer was not materialized in dedicated cache: %v", err)
	}
}

func TestRewriteInlineVisualizationsDisablesViewers(t *testing.T) {
	home := t.TempDir()
	threadDir := filepath.Join(home, "visualizations", "2026", "07", "21", "thread")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "chart.html"), []byte(`<canvas></canvas>`), 0o600); err != nil {
		t.Fatal(err)
	}
	context := &InlineVisualizationContext{
		VisualizationsDir: filepath.Join(home, "visualizations"),
		ThreadDir:         threadDir,
		DisableViewers:    true,
	}
	cleaned, items := RewriteInlineVisualizations("::codex-inline-vis{file=\"chart.html\"}", context)
	if len(items) != 0 || cleaned != "_Visualization unavailable on this device._" {
		t.Fatalf("disabled viewer rewrite = %q %#v", cleaned, items)
	}
}
