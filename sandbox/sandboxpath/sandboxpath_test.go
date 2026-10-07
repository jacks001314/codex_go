//go:build linux

package sandboxpath

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// writeFakeBwrapIn mirrors the Rust test fixture: an executable file named
// `bwrap` that the search treats as a candidate. It is never executed.
func writeFakeBwrapIn(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	path := filepath.Join(dir, "bwrap")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return resolved
}

// TestFindExecutableInPathSkipsWritableRootsLikeRust mirrors Rust
// `skips_bwrap_in_writable_roots_outside_command_cwd` (Rust #51211): a bwrap in
// another writable root is refused even when the command runs in a
// subdirectory, and a trusted directory is still found.
func TestFindExecutableInPathSkipsWritableRootsLikeRust(t *testing.T) {
	tempDir := t.TempDir()
	workspace := filepath.Join(tempDir, "workspace")
	cwd := filepath.Join(workspace, "service")
	workspaceBin := filepath.Join(workspace, "bin")
	extraRoot := filepath.Join(tempDir, "extra")
	trustedDir := filepath.Join(tempDir, "trusted")
	writeFakeBwrapIn(t, workspaceBin)
	writeFakeBwrapIn(t, extraRoot)
	expected := writeFakeBwrapIn(t, trustedDir)

	policy := FilesystemPolicy{WritableRoots: WritableRootsWithProtectedSubpaths([]string{workspace, extraRoot})}
	searchPath := workspaceBin + string(os.PathListSeparator) + extraRoot
	if got := FindExecutableInPath("bwrap", searchPath, cwd, policy); got != "" {
		t.Fatalf("FindExecutableInPath(writable roots) = %q, want none", got)
	}
	withTrusted := searchPath + string(os.PathListSeparator) + trustedDir
	if got := FindExecutableInPath("bwrap", withTrusted, cwd, policy); got != canonical(t, expected) {
		t.Fatalf("FindExecutableInPath(trusted) = %q, want %q", got, expected)
	}
}

// TestFindExecutableInPathSkipsSymlinkedWritableRootLikeRust mirrors Rust
// `skips_bwrap_in_symlinked_writable_root`: an alias of the workspace is just as
// writable, so the candidate under the canonical root is refused.
func TestFindExecutableInPathSkipsSymlinkedWritableRootLikeRust(t *testing.T) {
	tempDir := t.TempDir()
	workspace := filepath.Join(tempDir, "workspace")
	cwd := filepath.Join(workspace, "service")
	workspaceBin := filepath.Join(workspace, "bin")
	alias := filepath.Join(tempDir, "alias")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	writeFakeBwrapIn(t, workspaceBin)
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatalf("Symlink error = %v", err)
	}

	policy := FilesystemPolicy{WritableRoots: WritableRootsWithProtectedSubpaths([]string{alias})}
	if got := FindExecutableInPath("bwrap", workspaceBin, cwd, policy); got != "" {
		t.Fatalf("FindExecutableInPath(symlinked root) = %q, want none", got)
	}
}

// TestFindExecutableInPathRejectsOwnedBwrapOnFullDiskWriteLikeRust mirrors Rust
// `full_disk_write_rejects_owned_bwrap_even_when_chmod_read_only`: ownership
// alone is enough for the current user to chmod and replace the launcher.
func TestFindExecutableInPathRejectsOwnedBwrapOnFullDiskWriteLikeRust(t *testing.T) {
	tempDir := t.TempDir()
	bin := filepath.Join(tempDir, "bin")
	bwrap := writeFakeBwrapIn(t, bin)
	if err := os.Chmod(bwrap, 0o555); err != nil {
		t.Fatalf("Chmod error = %v", err)
	}

	policy := FilesystemPolicy{FullDiskWriteAccess: true}
	if got := FindExecutableInPath("bwrap", bin, "/", policy); got != "" {
		t.Fatalf("FindExecutableInPath(full disk write) = %q, want none", got)
	}
}

// TestFindExecutableInPathKeepsReadOnlyCarveoutWithReplaceableParentLikeRust
// mirrors Rust `read_only_bwrap_carveout_does_not_trust_a_replaceable_parent`:
// the carveout itself is protected, but its writable parent could still be
// renamed, so the candidate is refused.
func TestFindExecutableInPathKeepsReadOnlyCarveoutWithReplaceableParentLikeRust(t *testing.T) {
	tempDir := t.TempDir()
	bin := filepath.Join(tempDir, "bin")
	bwrap := writeFakeBwrapIn(t, bin)

	root := WritableRoot{
		Root:                   tempDir,
		ReadOnlySubpaths:       []string{bwrap},
		ProtectedMetadataNames: DefaultProtectedMetadataNames(),
	}
	policy := FilesystemPolicy{WritableRoots: []WritableRoot{root}}
	if got := FindExecutableInPath("bwrap", bin, "/", policy); got != "" {
		t.Fatalf("FindExecutableInPath(read-only carveout) = %q, want none", got)
	}
}

// TestFindExecutableInPathPreservesProtectedInstallationLikeRust mirrors Rust
// `root_write_preserves_a_read_only_system_installation`: a host-protected
// installation stays usable because the canonicalized candidate is outside
// every writable root and the current user cannot modify its ancestors.
func TestFindExecutableInPathPreservesProtectedInstallationLikeRust(t *testing.T) {
	systemBinary := canonical(t, "/bin/true")
	protectedRoot := ""
	for path := filepath.Dir(systemBinary); ; path = filepath.Dir(path) {
		if filepath.Dir(path) == "/" {
			protectedRoot = path
			break
		}
		if path == "/" {
			t.Fatalf("system binary %q is not below a top-level directory", systemBinary)
		}
	}

	tempDir := t.TempDir()
	link := filepath.Join(tempDir, "bwrap")
	if err := os.Symlink(systemBinary, link); err != nil {
		t.Fatalf("Symlink error = %v", err)
	}
	// Root write narrowed by a read-only carveout over the protected directory.
	policy := FilesystemPolicy{WritableRoots: []WritableRoot{{
		Root:                   "/",
		ReadOnlySubpaths:       []string{protectedRoot},
		ProtectedMetadataNames: DefaultProtectedMetadataNames(),
	}}}
	fullDiskWrite := FilesystemPolicy{FullDiskWriteAccess: true}

	for _, candidate := range []FilesystemPolicy{policy, fullDiskWrite} {
		// Root can replace the system installation unless the policy protects it;
		// everyone else keeps it, because the canonical candidate sits outside
		// every writable root and its ancestors are unmodifiable.
		want := systemBinary
		if candidate.FullDiskWriteAccess && os.Geteuid() == 0 {
			want = ""
		}
		if got := FindExecutableInPath("bwrap", tempDir, "/", candidate); got != want {
			t.Fatalf("FindExecutableInPath(protected installation, fullDisk=%v) = %q, want %q", candidate.FullDiskWriteAccess, got, want)
		}
	}
}

// TestCurrentUserCanModifyClassifiesAccessErrorsLikeRust mirrors Rust
// `current_user_can_modify`: only EACCES and EROFS prove the user cannot
// modify the path, and any unknown error keeps the candidate untrusted.
func TestCurrentUserCanModifyClassifiesAccessErrorsLikeRust(t *testing.T) {
	if !canModifyFromAccessError(nil) {
		t.Fatal("a successful access check must report modifiable")
	}
	if canModifyFromAccessError(unix.EACCES) {
		t.Fatal("EACCES must report not modifiable")
	}
	if canModifyFromAccessError(unix.EROFS) {
		t.Fatal("EROFS must report not modifiable")
	}
	if !canModifyFromAccessError(unix.EPERM) {
		t.Fatal("an unknown error must keep the candidate untrusted (modifiable)")
	}
	if !canModifyFromAccessError(unix.ENOSYS) {
		t.Fatal("a missing faccessat2 must keep the candidate untrusted (modifiable)")
	}

	tempDir := t.TempDir()
	owned := filepath.Join(tempDir, "owned")
	if err := os.WriteFile(owned, []byte("x"), 0o444); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if !CurrentUserCanModify(owned) {
		t.Fatal("the owner can chmod a read-only file, so it must report modifiable")
	}
	if !CurrentUserCanModify(filepath.Join(tempDir, "missing")) {
		t.Fatal("unreadable metadata must report modifiable")
	}
}

// TestWritableRootIsPathWritableLikeRust pins the policy predicate the filter
// and the sandbox mounts share.
func TestWritableRootIsPathWritableLikeRust(t *testing.T) {
	root := WritableRoot{
		Root:                   "/workspace",
		ReadOnlySubpaths:       []string{"/workspace/.git"},
		ProtectedMetadataNames: DefaultProtectedMetadataNames(),
	}
	cases := []struct {
		path string
		want bool
	}{
		{"/workspace/src/main.go", true},
		{"/workspace/.git/hooks/pre-commit", false},
		{"/workspace/.git", false},
		{"/workspace/.agents/x", false},
		{"/elsewhere/bwrap", false},
	}
	for _, testCase := range cases {
		if got := root.IsPathWritable(testCase.path); got != testCase.want {
			t.Errorf("IsPathWritable(%q) = %v, want %v", testCase.path, got, testCase.want)
		}
	}
}
