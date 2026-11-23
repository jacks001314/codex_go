package appserver

import (
	"os"
	"path/filepath"
	"testing"
)

// Rust #63088: when the root checkout's ".codex" (".gcode" here) is a regular
// file, reading the mapped project hook config fails with NotADirectory. That
// must count as a missing config, without a warning, so discovery still sees
// the worktree's own hooks and the matching nested folders.
func TestHookDiscoveryTreatsNotADirectoryAsMissingLikeRust(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "dot-codex-file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry := &HookListEntry{}
	appendHooksTOML(entry, &hookDiscoverySource{Path: filepath.Join(blocker, "config.toml"), Source: HookSourceProject})
	appendHooksJSON(entry, &hookDiscoverySource{Path: filepath.Join(blocker, "hooks.json"), Source: HookSourceProject})
	if len(entry.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want the non-directory base treated as missing", entry.Warnings)
	}

	// A genuinely unreadable path still reports its failure.
	dir := filepath.Join(base, "dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	appendHooksTOML(entry, &hookDiscoverySource{Path: dir, Source: HookSourceProject})
	if len(entry.Warnings) != 1 {
		t.Fatalf("warnings = %#v, want a directory read failure", entry.Warnings)
	}
}
