//go:build windows

package win

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
)

func TestEnsureCodexAppRuntimePathsReadableGrantsEveryone(t *testing.T) {
	root := makeRuntimeDirs(t)
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("USERPROFILE", root)
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable("S-1-1-0", &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) != 0 {
		t.Fatalf("refreshErrors = %#v; log=%s", refreshErrors, log.String())
	}
	if log.Len() == 0 {
		t.Fatalf("expected grant log")
	}
}

func TestRuntimeDirEligibleSkipsReparsePoints(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", target, err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create directory symlink (needs privileges): %v", err)
	}
	if runtimeDirEligible(link) {
		t.Fatalf("runtimeDirEligible(%q) = true, want false for a reparse point", link)
	}
	if !runtimeDirEligible(target) {
		t.Fatalf("runtimeDirEligible(%q) = false, want true for a real directory", target)
	}
}

// Mirrors Rust's #49058 regression at the production entry point: the runtime
// app root and the managed runtime cache are still repaired when the workload
// nests them beyond the legacy Windows path limit.
func TestEnsureCodexAppRuntimePathsReadableHandlesLongRuntimeRoots(t *testing.T) {
	root := t.TempDir()
	for len(root) <= 280 {
		root = filepath.Join(root, strings.Repeat("a", 60))
	}
	appRoot := filepath.Join(root, "OpenAI", "Codex")
	runtimeRoot := filepath.Join(root, ".cache", "codex-runtimes")
	for _, dir := range []string{appRoot, runtimeRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("USERPROFILE", root)
	const sid = "S-1-5-21-10-20-30-40"
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable(sid, &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) != 0 {
		t.Fatalf("refreshErrors = %#v; log=%s", refreshErrors, log.String())
	}
	if log.Len() == 0 {
		t.Fatal("expected the long runtime roots to be granted read/execute")
	}
	for _, dir := range []string{appRoot, runtimeRoot} {
		allows, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{Path: dir, SID: sid, Mask: uint32(RuntimeReadExecuteMask)}, true)
		if err != nil {
			t.Fatalf("PathMaskAllows(%s) error = %v", dir, err)
		}
		if !allows {
			t.Fatalf("PathMaskAllows(%s) = false after repair, want true", dir)
		}
	}
}
