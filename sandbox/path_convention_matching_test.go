package sandbox

import (
	"runtime"
	"testing"

	"codex_go/utils"
)

// Mirrors Rust #41209: containment follows the executor's convention, not the
// Go host's. POSIX stays case-sensitive; Windows folds case and separators.
func TestPathWithinForConventionLikeRust(t *testing.T) {
	if pathWithinForConvention("/workspace/secret.env", "/workspace/SECRET.env", utils.ConventionPosix) {
		t.Fatal("POSIX containment folded case")
	}
	if !pathWithinForConvention("/workspace/app/secret.env", "/workspace", utils.ConventionPosix) {
		t.Fatal("POSIX subtree containment was not recognized")
	}
	if !pathWithinForConvention(`c:/WORKSPACE/App/.ENV`, `C:\workspace`, utils.ConventionWindows) {
		t.Fatal("Windows containment did not fold case and separators")
	}
	if pathWithinForConvention(`C:\workspace-other\a.env`, `C:\workspace`, utils.ConventionWindows) {
		t.Fatal("Windows containment matched a sibling prefix")
	}
}

// The access resolver uses the same convention-aware containment for exact
// path entries.
func TestResolveFileSystemEntryAccessUsesExecutorConventionLikeRust(t *testing.T) {
	entries := []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "path", Path: "/workspace/SECRET.env"},
		Access: FileSystemAccessDeny,
	}}
	if got := ResolveFileSystemEntryAccessForConvention(entries, "/workspace/secret.env", "/workspace", utils.ConventionPosix); got != "" {
		t.Fatalf("POSIX access = %q, want no match", got)
	}
	if got := ResolveFileSystemEntryAccessForConvention(entries, "/workspace/SECRET.env/child", "/workspace", utils.ConventionPosix); got != FileSystemAccessDeny {
		t.Fatalf("POSIX subtree access = %q, want deny", got)
	}
	if runtime.GOOS == "windows" {
		windows := []FileSystemSandboxEntry{{
			Path:   FileSystemPath{Type: "path", Path: `C:\workspace\SECRET.env`},
			Access: FileSystemAccessDeny,
		}}
		if got := ResolveFileSystemEntryAccessForConvention(windows, `c:/WORKSPACE/secret.env`, `C:\workspace`, utils.ConventionWindows); got != FileSystemAccessDeny {
			t.Fatalf("Windows access = %q, want case-insensitive deny", got)
		}
	}
}
