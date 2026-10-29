//go:build windows

package win

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
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

func TestEnsureRuntimeTreeRepairsExistingChildren(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "OpenAI", "Codex", "runtimes")
	childDir := filepath.Join(runtimeRoot, "bin")
	childFile := filepath.Join(childDir, "runtime.js")
	if err := os.MkdirAll(childDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", childDir, err)
	}
	if err := os.WriteFile(childFile, []byte("runtime"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", childFile, err)
	}
	t.Setenv("LOCALAPPDATA", root)
	const sid = "S-1-1-0"
	var refreshErrors []string
	var log bytes.Buffer
	if err := EnsureCodexAppRuntimePathsReadable(sid, &refreshErrors, &log); err != nil {
		t.Fatalf("EnsureCodexAppRuntimePathsReadable() error = %v", err)
	}
	if len(refreshErrors) != 0 {
		t.Fatalf("refreshErrors = %#v; log=%s", refreshErrors, log.String())
	}
	for _, path := range []string{childDir, childFile} {
		allows, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{
			Path: path,
			SID:  sid,
			Mask: uint32(RuntimeReadExecuteMask),
		}, true)
		if err != nil {
			t.Fatalf("PathMaskAllows(%s) error = %v", path, err)
		}
		if !allows {
			t.Fatalf("PathMaskAllows(%s) = false after runtime tree repair", path)
		}
	}
}

// Mirrors Rust's #51822 regression: repairing the runtime tree must not open an
// active node.exe with access that conflicts with a reader holding it with
// FILE_SHARE_READ only, and it must keep existing deny ACEs instead of adding
// allows ahead of them.
func TestEnsureRuntimeTreeRepairsHeldRuntimeFileAndPreservesDenies(t *testing.T) {
	root := t.TempDir()
	node := filepath.Join(root, "bin", "node.exe")
	module := filepath.Join(root, "sky", "dist", "project", "index.js")
	for _, path := range []string{node, module} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("runtime content"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	const sid = "S-1-5-21-10-20-30-40"
	readExecute := uint32(RuntimeReadExecuteMask)
	// Root-only grant: without inheritance the children stay unreadable until
	// the tree repair visits them.
	if err := windowssandbox.EnsureAllowMaskACEsWithInheritance(windowssandbox.ACLRequest{
		Path: root,
		SID:  sid,
		Mask: readExecute,
	}, 0); err != nil {
		t.Fatalf("EnsureAllowMaskACEsWithInheritance(%s) error = %v", root, err)
	}
	assertMaskAllows(t, root, sid, readExecute, true, true)
	for _, path := range []string{node, module} {
		assertMaskAllows(t, path, sid, readExecute, true, false)
	}

	deniedDirectory := filepath.Join(root, "denied")
	if err := os.Mkdir(deniedDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(%s) error = %v", deniedDirectory, err)
	}
	if err := windowssandbox.AddDenyReadACE(windowssandbox.ACLRequest{Path: deniedDirectory, SID: sid}); err != nil {
		t.Fatalf("AddDenyReadACE(%s) error = %v", deniedDirectory, err)
	}
	deniedChild := filepath.Join(deniedDirectory, "private.js")
	if err := os.WriteFile(deniedChild, []byte("private runtime content"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", deniedChild, err)
	}
	writeDenied := filepath.Join(root, "read-only.js")
	if err := os.WriteFile(writeDenied, []byte("read-only runtime content"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", writeDenied, err)
	}
	if err := windowssandbox.AddDenyWriteACE(windowssandbox.ACLRequest{Path: writeDenied, SID: sid}); err != nil {
		t.Fatalf("AddDenyWriteACE(%s) error = %v", writeDenied, err)
	}

	// Keep data-write and delete opens blocked throughout repair and revalidation.
	reader := openReadOnlyShared(t, node)
	defer windows.CloseHandle(reader)

	writeOrACL := uint32(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.DELETE | windows.WRITE_DAC)
	for attempt := 0; attempt < 2; attempt++ {
		if err := ensureRuntimeTreeReadable(root, sid); err != nil {
			t.Fatalf("ensureRuntimeTreeReadable(attempt %d) error = %v", attempt, err)
		}
		for _, path := range []string{deniedDirectory, deniedChild} {
			denied, err := windowssandbox.PathMaskDenied(windowssandbox.ACLRequest{Path: path, SID: sid, Mask: readExecute})
			if err != nil {
				t.Fatalf("PathMaskDenied(%s) error = %v", path, err)
			}
			if !denied {
				t.Fatalf("read denial on %s did not survive repair", path)
			}
			assertMaskAllows(t, path, sid, readExecute, false, false)
		}
		denied, err := windowssandbox.PathMaskDenied(windowssandbox.ACLRequest{
			Path: writeDenied,
			SID:  sid,
			Mask: uint32(windows.FILE_GENERIC_WRITE | windows.DELETE),
		})
		if err != nil {
			t.Fatalf("PathMaskDenied(%s) error = %v", writeDenied, err)
		}
		if !denied {
			t.Fatalf("write denial on %s did not survive repair", writeDenied)
		}
		for _, path := range []string{node, module, filepath.Dir(module)} {
			assertMaskAllows(t, path, sid, readExecute, true, true)
			assertMaskAllows(t, path, sid, writeOrACL, false, false)
		}
	}
}

func openReadOnlyShared(t *testing.T, path string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%s) error = %v", path, err)
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile(%s) error = %v", path, err)
	}
	return handle
}

func assertMaskAllows(t *testing.T, path string, sid string, mask uint32, requireAllBits bool, want bool) {
	t.Helper()
	allows, err := windowssandbox.PathMaskAllows(windowssandbox.ACLRequest{Path: path, SID: sid, Mask: mask}, requireAllBits)
	if err != nil {
		t.Fatalf("PathMaskAllows(%s) error = %v", path, err)
	}
	if allows != want {
		t.Fatalf("PathMaskAllows(%s) = %v, want %v", path, allows, want)
	}
}
