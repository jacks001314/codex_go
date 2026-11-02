package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust parity: #58436 (0ffae14a43) viewer_embeds_interactive_controls. The
// viewer ships a tab runtime, a `.viz-carousel` variant picker with
// previous/next controls and live announcements, and the control styles the
// fragment markup relies on.
func TestInlineVisualizationViewerEmbedsInteractiveControlsLikeRust(t *testing.T) {
	home := t.TempDir()
	threadDir := filepath.Join(home, "visualizations", "2026", "11", "02", "thread")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fragment := `<nav class="nav nav-pills" role="tablist">
  <button class="nav-link btn-sm" role="tab" aria-controls="costs" data-tooltip="Show costs">Costs</button>
</nav>
<section id="costs" role="tabpanel" class="viz-carousel">
  <div data-variant="Daily">Day</div><div data-variant="Weekly">Week</div>
</section>`
	if err := os.WriteFile(filepath.Join(threadDir, "chart.html"), []byte(fragment), 0o600); err != nil {
		t.Fatal(err)
	}
	context := &InlineVisualizationContext{VisualizationsDir: filepath.Join(home, "visualizations"), ThreadDir: threadDir}
	_, items := RewriteInlineVisualizations(`::codex-inline-vis{file="chart.html"}`, context)
	if len(items) != 1 {
		t.Fatalf("visualizations = %#v", items)
	}
	viewer, err := os.ReadFile(filepath.FromSlash(strings.TrimPrefix(items[0].URL, "file://")))
	if err != nil {
		t.Fatal(err)
	}
	outer := string(viewer)
	document := htmlUnescapeViewer(t, outer)

	// The fragment still reaches the frame verbatim so generated markup keeps
	// its own structure and scripts.
	if !strings.Contains(document, `<section id="costs" role="tabpanel" class="viz-carousel">`) {
		t.Fatalf("fragment was rewritten:\n%s", document)
	}
	for _, want := range []string{
		`<script id="codex-visualization-tabs">`,
		`class="viz-carousel-previous"`,
		`class="viz-carousel-next"`,
		`class="viz-carousel-picker"`,
		`class="viz-carousel-options"`,
		`aria-live`,
		`[data-variant]`,
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("viewer runtime missing %q", want)
		}
	}
	for _, want := range []string{
		`.btn-sm{`,
		`.nav-pills .nav-link.active{`,
		`.viz-carousel > [data-variant][hidden]{`,
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("viewer styles missing %q", want)
		}
	}
	// The frame stays sandboxed: the controls run inside the sandboxed iframe
	// and never gain same-origin access.
	if strings.Contains(outer, "allow-same-origin") || !strings.Contains(outer, `sandbox="allow-scripts"`) {
		t.Fatalf("viewer sandbox contract changed:\n%s", outer)
	}
}

// htmlUnescapeViewer reverses the srcdoc escaping so the embedded runtime can be
// inspected the way Rust's snapshot test inspects the generated frame.
func htmlUnescapeViewer(t *testing.T, document string) string {
	t.Helper()
	_, srcdoc, ok := strings.Cut(document, ` srcdoc="`)
	if !ok {
		t.Fatalf("viewer document has no embedded frame:\n%s", document)
	}
	srcdoc, _, ok = strings.Cut(srcdoc, `"`)
	if !ok {
		t.Fatalf("viewer frame is not terminated")
	}
	replacer := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&#34;", `"`, "&quot;", `"`, "&#39;", "'", "&amp;", "&")
	return replacer.Replace(srcdoc)
}
