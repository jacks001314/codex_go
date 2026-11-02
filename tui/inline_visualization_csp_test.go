package tui

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const visualizationCSPHosts = "https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://esm.sh https://fonts.bunny.net https://fonts.googleapis.com https://fonts.gstatic.com https://unpkg.com"

// viewerDocumentCSP extracts the Content-Security-Policy meta directive of a
// generated viewer document.
func viewerDocumentCSP(document string) (string, bool) {
	const marker = `<meta http-equiv="Content-Security-Policy" content="`
	index := strings.Index(document, marker)
	if index < 0 {
		return "", false
	}
	rest := document[index+len(marker):]
	end := strings.Index(rest, `">`)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// parseViewerCSP splits a policy into directive -> source list.
func parseViewerCSP(policy string) map[string][]string {
	directives := map[string][]string{}
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		directives[fields[0]] = fields[1:]
	}
	return directives
}

// cspAllowsInlineScript reports whether an inline <script> may run under the
// policy: CSP3 intersects every applying policy, so a srcdoc document needs
// both its own policy and the inherited shell policy to permit it.
func cspAllowsInlineScript(policy string) bool {
	directives := parseViewerCSP(policy)
	sources, ok := directives["script-src"]
	if !ok {
		sources = directives["default-src"]
	}
	for _, source := range sources {
		if source == "'unsafe-inline'" {
			return true
		}
	}
	return false
}

// cspHasBareHTTPSWildcard reports a scheme-only source such as `https:`, which
// would make img-src/font-src an arbitrary-host beacon channel (#58436 F2).
func cspHasBareHTTPSWildcard(policy string) bool {
	for _, token := range strings.Fields(strings.NewReplacer(";", " ").Replace(policy)) {
		if token == "https:" {
			return true
		}
	}
	return false
}

// viewerSrcdocFrame returns the decoded frame document carried by the shell's
// srcdoc attribute.
func viewerSrcdocFrame(document string) (string, bool) {
	const marker = `srcdoc="`
	index := strings.Index(document, marker)
	if index < 0 {
		return "", false
	}
	rest := document[index+len(marker):]
	end := strings.Index(rest, `"></iframe>`)
	if end < 0 {
		return "", false
	}
	return html.UnescapeString(rest[:end]), true
}

// Rust #58436: a srcdoc frame inherits its parent's CSP, so the shell must grant
// the scripts and CDN sources the frame needs while keeping the
// `default-src 'none'` baseline. The frame itself must stay narrower than the
// shell and must not accept arbitrary hosts.
func TestInlineVisualizationViewerCSPAllowsInjectedRuntimeLikeRust(t *testing.T) {
	document := inlineVisualizationViewerDocument(`<div class="viz-carousel"><div data-variant="a">A</div></div>`, "chart")
	shellCSP, ok := viewerDocumentCSP(document)
	if !ok {
		t.Fatalf("shell document has no CSP:\n%s", document)
	}
	frame, ok := viewerSrcdocFrame(document)
	if !ok {
		t.Fatalf("shell document has no srcdoc frame:\n%s", document)
	}
	frameCSP, ok := viewerDocumentCSP(frame)
	if !ok {
		t.Fatalf("frame document has no CSP:\n%s", frame)
	}

	// The injected runtime is an inline script, so the shell policy inherited by
	// the srcdoc frame must allow inline script execution.
	if !cspAllowsInlineScript(shellCSP) {
		t.Fatalf("shell CSP blocks the injected runtime (#58436 F1): %q", shellCSP)
	}
	if !cspAllowsInlineScript(frameCSP) {
		t.Fatalf("frame CSP blocks the injected runtime: %q", frameCSP)
	}

	shell := parseViewerCSP(shellCSP)
	if got := shell["default-src"]; len(got) != 1 || got[0] != "'none'" {
		t.Fatalf("shell default-src = %#v, want the 'none' baseline", got)
	}
	if got := shell["frame-src"]; len(got) != 1 || got[0] != "'self'" {
		t.Fatalf("shell frame-src = %#v, want 'self'", got)
	}
	for _, source := range []string{"'unsafe-inline'", "'unsafe-eval'", "'wasm-unsafe-eval'"} {
		if !containsToken(shell["script-src"], source) {
			t.Fatalf("shell script-src = %#v, missing %s", shell["script-src"], source)
		}
	}
	for _, directive := range []string{"media-src", "worker-src"} {
		if _, ok := shell[directive]; !ok {
			t.Fatalf("shell CSP is missing %s: %q", directive, shellCSP)
		}
	}

	frameDirectives := parseViewerCSP(frameCSP)
	if got := frameDirectives["frame-src"]; len(got) != 1 || got[0] != "'none'" {
		t.Fatalf("frame frame-src = %#v, want 'none'", got)
	}
	for _, directive := range []string{"script-src", "style-src", "img-src", "font-src"} {
		for _, host := range strings.Fields(visualizationCSPHosts) {
			if !containsToken(frameDirectives[directive], host) {
				t.Fatalf("%s = %#v, missing allowed host %s", directive, frameDirectives[directive], host)
			}
		}
	}
	if cspHasBareHTTPSWildcard(shellCSP) || cspHasBareHTTPSWildcard(frameCSP) {
		t.Fatalf("viewer CSP still accepts arbitrary https hosts (#58436 F2): shell=%q frame=%q", shellCSP, frameCSP)
	}
}

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

// Rust #58436: the generated document must actually carry the interactive
// runtime inside the sandboxed frame, and that script must be executable rather
// than only present as text.
func TestInlineVisualizationViewerEmbedsExecutableRuntimeLikeRust(t *testing.T) {
	fragment := `<div class="nav" role="tablist"><button role="tab" aria-controls="p1" aria-selected="true">One</button></div><div id="p1" role="tabpanel">Body</div>`
	document := inlineVisualizationViewerDocument(fragment, "chart")
	if !strings.Contains(document, `sandbox="allow-scripts"`) {
		t.Fatalf("frame is not sandboxed with allow-scripts:\n%s", document)
	}
	if strings.Contains(document, "allow-same-origin") {
		t.Fatalf("frame must not keep allow-same-origin:\n%s", document)
	}
	frame, ok := viewerSrcdocFrame(document)
	if !ok {
		t.Fatalf("shell document has no srcdoc frame:\n%s", document)
	}
	const scriptOpen = `<script id="codex-visualization-tabs">`
	scriptStart := strings.Index(frame, scriptOpen)
	if scriptStart < 0 {
		t.Fatalf("frame does not reference the injected runtime:\n%s", frame)
	}
	if strings.Index(frame, fragment) > scriptStart {
		t.Fatalf("runtime runs before the visualization markup:\n%s", frame)
	}
	scriptEnd := strings.Index(frame[scriptStart:], `</script>`)
	if scriptEnd < 0 {
		t.Fatalf("runtime script is not closed:\n%s", frame)
	}
	runtime := frame[scriptStart+len(scriptOpen) : scriptStart+scriptEnd]
	for _, want := range []string{`[role="tablist"]`, ".viz-carousel", "DOMContentLoaded"} {
		if !strings.Contains(runtime, want) {
			t.Fatalf("runtime is missing %q:\n%s", want, runtime)
		}
	}
	runInlineVisualizationRuntime(t, runtime)
}

// runInlineVisualizationRuntime executes the embedded runtime under node with a
// minimal DOM shim, so a document whose CSP would block (or whose script would
// throw) is caught instead of only matching a literal string.
func runInlineVisualizationRuntime(t *testing.T, source string) {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the CSP assertions above still guard the document")
	}
	shim := "globalThis.window = globalThis;\n" +
		"globalThis.addEventListener = function () {};\n" +
		"globalThis.document = { readyState: \"loading\", querySelectorAll: function () { return []; }, addEventListener: function () {} };\n"
	scriptPath := filepath.Join(t.TempDir(), "viewer-runtime.js")
	if err := os.WriteFile(scriptPath, []byte(shim+source), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(nodePath, scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("the embedded viewer runtime is not executable: %v\n%s", err, output)
	}
}

// Rust #58436 viewer.rs: the materialized viewer cache must resolve to itself,
// so a symbolic link cannot redirect the document outside the cache.
func TestInlineVisualizationViewerRejectsRedirectedCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	threadDir := filepath.Join(home, "visualizations", "thread")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "chart.html"), []byte("<p>chart</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	viewerDir := filepath.Join(home, "viewer-cache")
	redirected := filepath.Join(home, "outside-cache")
	original := evalViewerSymlinks
	evalViewerSymlinks = func(path string) (string, error) {
		if filepath.Clean(path) == filepath.Clean(viewerDir) {
			return redirected, nil
		}
		return original(path)
	}
	defer func() { evalViewerSymlinks = original }()
	context := &InlineVisualizationContext{
		VisualizationsDir: filepath.Join(home, "visualizations"),
		ThreadDir:         threadDir,
		ViewerDir:         viewerDir,
	}
	cleaned, items := RewriteInlineVisualizations(`::codex-inline-vis{file="chart.html"}`, context)
	if len(items) != 0 || cleaned != "_Visualization unavailable on this device._" {
		t.Fatalf("a redirected viewer cache was accepted: %q %#v", cleaned, items)
	}
	if _, err := os.Stat(filepath.Join(redirected, "chart.html")); err == nil {
		t.Fatal("the viewer document was written outside the cache")
	}
}

func TestInlineVisualizationViewerRejectsSymlinkedCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	threadDir := filepath.Join(home, "visualizations", "thread")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "chart.html"), []byte("<p>chart</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "outside-cache")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "viewer-cache")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this platform cannot create symbolic links: %v", err)
	}
	context := &InlineVisualizationContext{
		VisualizationsDir: filepath.Join(home, "visualizations"),
		ThreadDir:         threadDir,
		ViewerDir:         link,
	}
	cleaned, items := RewriteInlineVisualizations(`::codex-inline-vis{file="chart.html"}`, context)
	if len(items) != 0 || cleaned != "_Visualization unavailable on this device._" {
		t.Fatalf("a symlinked viewer cache was accepted: %q %#v", cleaned, items)
	}
	if entries, err := os.ReadDir(target); err == nil && len(entries) != 0 {
		t.Fatalf("a viewer document was written through the symlink: %#v", entries)
	}
}
