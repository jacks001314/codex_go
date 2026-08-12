//go:build windows

package win

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
