package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExecServerPathOnlyResolvesAdvertisedFilesLikeRust covers Rust #57590's
// "verify only when advertised" installer rule.
func TestExecServerPathOnlyResolvesAdvertisedFilesLikeRust(t *testing.T) {
	dir := t.TempDir()
	layout := &CodexPackageLayout{PackageDir: dir}
	context := &InstallContext{PackageLayout: layout}

	writeManifest := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, packageMetadataFilename), []byte(body), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}

	writeManifest(`{"version":"1.2.3"}`)
	if path, ok := context.ExecServerPath(); ok || path != "" {
		t.Fatalf("legacy package resolved executor %q, ok=%v", path, ok)
	}

	writeManifest(`{"version":"1.2.3","execServer":"bin/codex-exec-server"}`)
	if path, ok := context.ExecServerPath(); ok || path != "" {
		t.Fatalf("missing executor resolved %q, ok=%v", path, ok)
	}

	execPath := filepath.Join(dir, "bin", "codex-exec-server")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("executor"), 0o755); err != nil {
		t.Fatalf("write executor: %v", err)
	}
	if path, ok := context.ExecServerPath(); !ok || path != execPath {
		t.Fatalf("advertised executor = %q, ok=%v, want %q", path, ok, execPath)
	}
}
