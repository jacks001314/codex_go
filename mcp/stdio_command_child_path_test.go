package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveBareCommandInChildPathLikeRust covers Rust #42192's bare-command
// search: the child's configured PATH wins, an empty entry means the current
// directory, and a failed search keeps the bare spelling for the regular
// launcher.
func TestResolveBareCommandInChildPathLikeRust(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "mcp-test-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if got := resolveBareCommandInChildPath("mcp-test-tool", map[string]string{"PATH": dir}); got != tool {
		t.Fatalf("resolveBareCommandInChildPath() = %q, want %q", got, tool)
	}
	if got := resolveBareCommandInChildPath("missing-tool", map[string]string{"PATH": dir}); got != "missing-tool" {
		t.Fatalf("resolveBareCommandInChildPath(missing) = %q, want the bare spelling", got)
	}
	if got := resolveBareCommandInChildPath("mcp-test-tool", map[string]string{"PATH": filepath.Join(dir, "nested")}); got != "mcp-test-tool" {
		t.Fatalf("resolveBareCommandInChildPath(nested) = %q, want the bare spelling", got)
	}
}

// TestResolveBareCommandInChildPathEmptyEntryMeansCWDLikeRust follows execvp's
// empty-entry rule without depending on the host separator style.
func TestResolveBareCommandInChildPathEmptyEntryMeansCWDLikeRust(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	tool := filepath.Join(dir, "cwd-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	separator := string(os.PathListSeparator)
	for _, pathValue := range []string{"", separator, "/nonexistent" + separator, separator + "/nonexistent"} {
		got := resolveBareCommandInChildPath("cwd-tool", map[string]string{"PATH": pathValue})
		want := filepath.Join(".", "cwd-tool")
		if got != want {
			t.Fatalf("PATH=%q resolved to %q, want %q", pathValue, got, want)
		}
	}
}

// TestResolveBareCommandInChildPathDefaultPathLikeRust uses Rust's /usr/bin:/bin
// default when the child environment carries no PATH.
func TestResolveBareCommandInChildPathDefaultPathLikeRust(t *testing.T) {
	want := ""
	for _, candidate := range []string{"/usr/bin/sh", "/bin/sh"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			want = candidate
			break
		}
	}
	if want == "" {
		t.Skip("no POSIX shell in the default search path")
	}
	if got := resolveBareCommandInChildPath("sh", nil); got != want {
		t.Fatalf("resolveBareCommandInChildPath(sh, nil) = %q, want %q", got, want)
	}
	if got := resolveBareCommandInChildPath("sh", map[string]string{}); got != want {
		t.Fatalf("resolveBareCommandInChildPath(sh, empty) = %q, want %q", got, want)
	}
}
