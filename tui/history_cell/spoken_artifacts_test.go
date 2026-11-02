package historycell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/tui"
)

// Rust 3caf9f9586 `trusted_workspace_files_require_confined_regular_inert_sources`:
// only existing, allowed-extension, non-hidden files inside the canonical
// workspace become spoken artifact links.
func TestTrustedWorkspaceFilesRequireConfinedRegularInertSources(t *testing.T) {
	workspace := t.TempDir()
	sourceDirectory := filepath.Join(workspace, "src")
	if err := os.MkdirAll(sourceDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile := func(name string, content string) string {
		t.Helper()
		path := filepath.Join(sourceDirectory, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeFile("safe.go", "package safe")
	writeFile("run.py", "print('no')")
	writeFile(".hidden.go", "hidden")
	if err := os.Mkdir(filepath.Join(sourceDirectory, "directory.go"), 0o755); err != nil {
		t.Fatal(err)
	}

	file, ok := ValidateTrustedWorkspaceFile(workspace, "src/safe.go")
	if !ok {
		t.Fatal("a workspace source was rejected")
	}
	if !strings.HasPrefix(file.URL, "file://") || !strings.HasSuffix(file.URL, "/src/safe.go") {
		t.Fatalf("artifact URL = %q", file.URL)
	}
	windowsFile, ok := ValidateTrustedWorkspaceFile(workspace, `src\safe.go`)
	if !ok || windowsFile.URL != file.URL {
		t.Fatalf("relative Windows separators were not trusted: %#v", windowsFile)
	}

	for _, candidate := range []string{
		"../safe.go",
		"src/../src/safe.go",
		"/src/safe.go",
		`src\\safe.go`,
		`src\..\safe.go`,
		`\src\safe.go`,
		`\\server\share\safe.go`,
		`C:\src\safe.go`,
		"file://src/safe.go",
		"src/safe.go\u0007",
		"src/.hidden.go",
		"src/missing.go",
		"src/directory.go",
		"src/run.py",
		"",
	} {
		if _, ok := ValidateTrustedWorkspaceFile(workspace, candidate); ok {
			t.Fatalf("unsafe artifact was accepted: %q", candidate)
		}
	}
}

func TestTrustedWorkspaceFileRejectsSymlinkEscapes(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsideFile, []byte("package outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(workspace, "src", "escape.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	disguised := filepath.Join(workspace, "src", "run.py")
	if err := os.WriteFile(disguised, []byte("print('no')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(disguised, filepath.Join(workspace, "src", "disguised.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, candidate := range []string{"src/escape.go", "src/disguised.go"} {
		if _, ok := ValidateTrustedWorkspaceFile(workspace, candidate); ok {
			t.Fatalf("symlink escape was accepted: %q", candidate)
		}
	}
}

// Rust spoken artifact rendering underlines and links file references, keeps
// the surrounding prose, trims trailing punctuation and :line suffixes, and
// leaves lines that already carry hyperlinks untouched.
func TestAnnotateSpokenArtifactsLinksWorkspaceFiles(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "lib.go"), []byte("package lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# readme"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines := AnnotateSpokenArtifacts([]string{
		"* Assistant: fixed src/lib.go:42 and README.md.",
		"* nothing to link in src/missing.go",
	}, workspace)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.Contains(lines[0], "\x1b[4m") || !strings.Contains(lines[0], "\x1b[24m") {
		t.Fatalf("artifact was not underlined: %q", lines[0])
	}
	if !strings.Contains(lines[0], tui.OSC8Hyperlink(fileURLForTest(t, workspace, "src/lib.go"), "src/lib.go")) {
		t.Fatalf("src/lib.go was not linked: %q", lines[0])
	}
	if !strings.Contains(lines[0], tui.OSC8Hyperlink(fileURLForTest(t, workspace, "README.md"), "README.md")) {
		t.Fatalf("README.md was not linked: %q", lines[0])
	}
	// The :42 suffix stays visible but outside the link, and the prose is kept.
	visible := strings.ReplaceAll(lines[0], "\x1b[4m", "")
	visible = strings.ReplaceAll(visible, "\x1b[24m", "")
	if !strings.Contains(visible, "fixed ") || !strings.Contains(visible, " and README.md.") {
		t.Fatalf("prose changed: %q", visible)
	}
	if strings.Contains(lines[1], "http") || strings.Contains(lines[1], "]8;;") {
		t.Fatalf("a missing file was linked: %q", lines[1])
	}

	existing := "already " + tui.OSC8Hyperlink("https://example.com", "linked") + " src/lib.go"
	preserved := AnnotateSpokenArtifacts([]string{existing}, workspace)
	if preserved[0] != existing {
		t.Fatalf("existing hyperlink line was rewritten: %q", preserved[0])
	}
}

func fileURLForTest(t *testing.T, workspace string, relative string) string {
	t.Helper()
	file, ok := ValidateTrustedWorkspaceFile(workspace, relative)
	if !ok {
		t.Fatalf("%q was rejected", relative)
	}
	return file.URL
}

// The assistant caption links artifacts; a user prompt never does, and an
// empty workspace (remote session) leaves every reference as plain text.
func TestSpokenAssistantCellLinksArtifacts(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	assistant := NewSpokenAssistantCell("wrote notes.md", workspace)
	joined := strings.Join(assistant.DisplayLines(80), "\n")
	if !strings.Contains(joined, tui.OSC8Hyperlink(fileURLForTest(t, workspace, "notes.md"), "notes.md")) {
		t.Fatalf("assistant artifact was not linked: %q", joined)
	}
	if raw := strings.Join(assistant.RawLines(), "\n"); strings.Contains(raw, "]8;;") {
		t.Fatalf("raw lines carried a hyperlink: %q", raw)
	}

	remote := NewSpokenAssistantCell("wrote notes.md", "")
	if joined := strings.Join(remote.DisplayLines(80), "\n"); strings.Contains(joined, "]8;;") {
		t.Fatalf("remote caption linked a workspace file: %q", joined)
	}

	user := NewSpokenUserPrompt("wrote notes.md")
	if joined := strings.Join(user.DisplayLines(80), "\n"); strings.Contains(joined, "]8;;") {
		t.Fatalf("spoken user prompt linked an artifact: %q", joined)
	}
}
