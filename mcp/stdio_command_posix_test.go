//go:build !windows

package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestResolveMCPStdioCommandUsesChildPathLikeRust mirrors Rust #42192: a bare
// command resolves against the child's configured PATH on POSIX launchers.
func TestResolveMCPStdioCommandUsesChildPathLikeRust(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "mcp-test-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if got := resolveMCPStdioCommand("mcp-test-tool", map[string]string{"PATH": dir}); got != tool {
		t.Fatalf("resolveMCPStdioCommand() = %q, want %q", got, tool)
	}
	// A non-executable file is not a match.
	plain := filepath.Join(dir, "plain-file")
	if err := os.WriteFile(plain, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if got := resolveMCPStdioCommand("plain-file", map[string]string{"PATH": dir}); got != "plain-file" {
		t.Fatalf("resolveMCPStdioCommand() = %q, want the bare spelling", got)
	}
}

// TestResolveMCPStdioCommandKeepsExplicitPathsLikeRust leaves commands that
// already name a path or extension untouched on POSIX.
func TestResolveMCPStdioCommandKeepsExplicitPathsLikeRust(t *testing.T) {
	for _, command := range []string{"./server", "bin/server", "/opt/server", "server.py"} {
		if got := resolveMCPStdioCommand(command, map[string]string{"PATH": t.TempDir()}); got != command {
			t.Fatalf("resolveMCPStdioCommand(%q) = %q, want it unchanged", command, got)
		}
	}
}

// TestPreserveMCPStdioArgv0LikeRust keeps the configured spelling as argv[0]
// while the resolved executor path drives the launch.
func TestPreserveMCPStdioArgv0LikeRust(t *testing.T) {
	resolved := "/opt/tools/mcp-server"
	cmd := exec.Command(resolved, "--stdio")
	preserveMCPStdioArgv0(cmd, "mcp-server", resolved)
	if cmd.Args[0] != "mcp-server" {
		t.Fatalf("argv[0] = %q, want the configured spelling", cmd.Args[0])
	}
	if cmd.Path != resolved {
		t.Fatalf("path = %q, want the resolved executor path", cmd.Path)
	}
	unchanged := exec.Command(resolved, "--stdio")
	preserveMCPStdioArgv0(unchanged, resolved, resolved)
	if unchanged.Args[0] != resolved {
		t.Fatalf("argv[0] = %q, want it untouched for an explicit path", unchanged.Args[0])
	}
}
