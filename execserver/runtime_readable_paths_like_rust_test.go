package execserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExecServerRuntimeReadablePathsLikeRust mirrors Rust #60663: the execution
// host grants restricted reads to its own helper executable and the absolute
// CA bundle selected by the child environment, while relative paths are ignored.
func TestExecServerRuntimeReadablePathsLikeRust(t *testing.T) {
	directory := t.TempDir()
	caBundle := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caBundle, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := execServerRuntimeReadablePaths(&ExecParams{Env: map[string]string{
		"SSL_CERT_FILE":        caBundle,
		"CODEX_CA_CERTIFICATE": "relative-ca.pem",
	}})
	seen := map[string]bool{}
	for _, path := range paths {
		seen[path] = true
	}
	if !seen[filepath.Clean(caBundle)] {
		t.Fatalf("runtime readable paths = %#v, want CA bundle %q", paths, caBundle)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			t.Fatalf("runtime readable path is not absolute: %q", path)
		}
	}
	if runtime.GOOS == "linux" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if absolute, absErr := filepath.Abs(executable); absErr == nil && !seen[filepath.Clean(absolute)] {
			t.Fatalf("runtime readable paths = %#v, want helper executable %q", paths, absolute)
		}
	}

	duplicate := execServerRuntimeReadablePaths(&ExecParams{Env: map[string]string{
		"SSL_CERT_FILE":        caBundle,
		"CODEX_CA_CERTIFICATE": caBundle,
	}})
	count := 0
	for _, path := range duplicate {
		if path == filepath.Clean(caBundle) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate CA bundle granted %d times: %#v", count, duplicate)
	}
}
