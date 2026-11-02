package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rust #58436 viewer.rs: a srcdoc frame inherits the shell document's CSP, so
// the pre-fix shell policy (`default-src 'none'; style-src 'unsafe-inline';
// frame-src 'self'`) silently blocked the injected tab/carousel runtime. No
// string assertion or DOM shim can observe that: the document is well formed
// and the script text is present, it simply never executes.
//
// This A/B runs the real document in headless Chrome: `before` is the current
// document with the pre-fix shell policy substituted, `after` is what the TUI
// materializes today. The probe reports `blocked` for the broken policy and
// `live_controls` when the runtime actually produced `.viz-carousel-controls`
// inside the sandboxed frame.
const inlineVisualizationPreFixShellCSP = "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'"

func TestInlineVisualizationViewerCSPABInRealChromeLikeRust(t *testing.T) {
	chrome := findChromeForVisualizationTest()
	if chrome == "" {
		t.Skip("Chrome is not installed; set CODEX_CHROME_PATH to run the real-browser A/B check")
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the browser harness drives Chrome over the DevTools protocol")
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	harness := filepath.Join(filepath.Dir(filename), "..", "scripts", "inline_visualization_csp_ab.mjs")
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("the A/B harness is missing: %v", err)
	}

	fragment := `<div class="viz-carousel"><div data-variant="a">First</div><div data-variant="b">Second</div></div>` +
		`<div class="nav" role="tablist"><button role="tab" aria-controls="p1" aria-selected="true">One</button>` +
		`<button role="tab" aria-controls="p2">Two</button></div>` +
		`<div id="p1" role="tabpanel">One body</div><div id="p2" role="tabpanel" hidden>Two body</div>`
	after := inlineVisualizationViewerDocument(fragment, "csp-ab")
	shell := inlineVisualizationShellCSP()
	if !strings.Contains(after, shell) {
		t.Fatalf("the current document does not carry the shell policy %q", shell)
	}
	before := strings.Replace(after, shell, inlineVisualizationPreFixShellCSP, 1)
	if before == after {
		t.Fatal("could not reproduce the pre-fix shell policy")
	}

	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.html")
	afterPath := filepath.Join(dir, "after.html")
	if err := os.WriteFile(beforePath, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(afterPath, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command(nodePath, harness,
		"--chrome", chrome,
		"--before", beforePath,
		"--after", afterPath,
		"--timeout-ms", "8000",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("the Chrome A/B harness failed: %v\n%s", err, output)
	}
	line := lastNonEmptyLine(string(output))
	var verdict struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if err := json.Unmarshal([]byte(line), &verdict); err != nil {
		t.Fatalf("the harness did not report a verdict: %v\n%s", err, output)
	}
	if verdict.Before != "blocked" {
		t.Fatalf("the pre-fix shell CSP did not block the runtime in Chrome: %q\n%s", verdict.Before, output)
	}
	if verdict.After != "live_controls" {
		t.Fatalf("the fixed shell CSP left the viewer without its live controls: %q\n%s", verdict.After, output)
	}
}

// findChromeForVisualizationTest resolves a Chrome/Chromium binary the A/B
// harness can drive; an empty result means the caller skips instead of failing.
func findChromeForVisualizationTest() string {
	if configured := strings.TrimSpace(os.Getenv("CODEX_CHROME_PATH")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
	default:
		candidates = append(candidates, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome")
	}
	for _, candidate := range candidates {
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line != "" {
			return line
		}
	}
	return ""
}
