package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
)

// TestProjectDocFallbackFilenamesDriveDiscoveryLikeRust mirrors Rust
// core/src/agents_md.rs::candidate_filenames: configured fallback filenames are
// tried after AGENTS.override.md/AGENTS.md during project discovery.
func TestProjectDocFallbackFilenamesDriveDiscoveryLikeRust(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git error = %v", err)
	}
	nested := filepath.Join(repo, "pkg", "sub")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested error = %v", err)
	}
	const fallbackDoc = "Fallback project instructions."
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte(fallbackDoc), 0o600); err != nil {
		t.Fatalf("write fallback doc error = %v", err)
	}

	router := &RuntimeRouter{}
	text, _, err := router.loadProjectInstructionsFor(nested, &config.Config{Values: map[string]any{}})
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor() error = %v", err)
	}
	if strings.Contains(text, fallbackDoc) {
		t.Fatalf("an unconfigured fallback filename was loaded: %q", text)
	}

	cfg := &config.Config{Values: map[string]any{
		"project_doc_fallback_filenames": []any{"CLAUDE.md"},
	}}
	text, sources, err := router.loadProjectInstructionsFor(nested, cfg)
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor() error = %v", err)
	}
	if !strings.Contains(text, fallbackDoc) {
		t.Fatalf("fallback doc not loaded: %q", text)
	}
	if len(sources) != 1 || !strings.HasSuffix(sources[0], "CLAUDE.md") {
		t.Fatalf("sources = %#v, want the CLAUDE.md path", sources)
	}
}

// TestProjectDocFallbackFilenamesRejectPathSyntaxLikeRust mirrors Rust #45865:
// configured fallback entries containing path syntax are ignored before any
// filesystem probe, so they can never reach a nested or network path, while
// valid entries alongside them still load.
func TestProjectDocFallbackFilenamesRejectPathSyntaxLikeRust(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git error = %v", err)
	}
	nested := filepath.Join(repo, "sub")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "CLAUDE.md"), []byte("Nested fallback."), 0o600); err != nil {
		t.Fatalf("write nested fallback error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("Root fallback."), 0o600); err != nil {
		t.Fatalf("write root fallback error = %v", err)
	}

	router := &RuntimeRouter{}
	invalid := &config.Config{Values: map[string]any{
		"project_doc_fallback_filenames": []any{"sub/CLAUDE.md", "..", ".", "nested/CLAUDE.md"},
	}}
	text, _, err := router.loadProjectInstructionsFor(repo, invalid)
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor(invalid) error = %v", err)
	}
	if strings.Contains(text, "Nested fallback.") {
		t.Fatalf("a fallback entry with path syntax reached the filesystem: %q", text)
	}

	valid := &config.Config{Values: map[string]any{
		"project_doc_fallback_filenames": []any{"sub/CLAUDE.md", "CLAUDE.md"},
	}}
	text, sources, err := router.loadProjectInstructionsFor(repo, valid)
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor(valid) error = %v", err)
	}
	if !strings.Contains(text, "Root fallback.") || strings.Contains(text, "Nested fallback.") {
		t.Fatalf("valid fallback entries did not load correctly: %q", text)
	}
	if len(sources) != 1 || !strings.HasSuffix(sources[0], "CLAUDE.md") {
		t.Fatalf("sources = %#v, want the root CLAUDE.md path", sources)
	}
}

// TestProjectDocFallbackFilenamesOverrideOrderLikeRust pins that AGENTS.md
// still wins over a configured fallback filename in the same directory.
func TestProjectDocFallbackFilenamesOverrideOrderLikeRust(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("AGENTS wins."), 0o600); err != nil {
		t.Fatalf("write AGENTS.md error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("CLAUDE loses."), 0o600); err != nil {
		t.Fatalf("write CLAUDE.md error = %v", err)
	}
	cfg := &config.Config{Values: map[string]any{
		"project_doc_fallback_filenames": []any{"CLAUDE.md"},
	}}
	text, _, err := (&RuntimeRouter{}).loadProjectInstructionsFor(repo, cfg)
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor() error = %v", err)
	}
	if !strings.Contains(text, "AGENTS wins.") || strings.Contains(text, "CLAUDE loses.") {
		t.Fatalf("candidate order = %q, want AGENTS.md first", text)
	}
}

// TestProjectRootMarkersDriveDiscoveryLikeRust covers the configured
// project_root_markers used to find the repository root.
func TestProjectRootMarkersDriveDiscoveryLikeRust(t *testing.T) {
	repo := t.TempDir()
	const doc = "Marker-rooted project instructions."
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(doc), 0o600); err != nil {
		t.Fatalf("write AGENTS.md error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "PROJECT_MARKER"), []byte("marker"), 0o600); err != nil {
		t.Fatalf("write marker error = %v", err)
	}
	nested := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested error = %v", err)
	}

	text, _, err := (&RuntimeRouter{}).loadProjectInstructionsFor(nested, &config.Config{Values: map[string]any{}})
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor() error = %v", err)
	}
	if strings.Contains(text, doc) {
		t.Fatalf("default markers found the marker-rooted doc: %q", text)
	}

	cfg := &config.Config{Values: map[string]any{"project_root_markers": []any{"PROJECT_MARKER"}}}
	text, _, err = (&RuntimeRouter{}).loadProjectInstructionsFor(nested, cfg)
	if err != nil {
		t.Fatalf("loadProjectInstructionsFor() error = %v", err)
	}
	if !strings.Contains(text, doc) {
		t.Fatalf("configured marker did not root the discovery: %q", text)
	}
}
