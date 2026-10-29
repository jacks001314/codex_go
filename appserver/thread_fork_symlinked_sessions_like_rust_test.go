package appserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Mirrors Rust #42135 `thread_fork_accepts_resumed_rollout_under_symlinked_sessions_root`:
// thread/fork must accept a paginated source whose rollout lives under a
// symlinked managed `sessions` root. The router validates lineage references
// with the canonical Codex home roots, so the symlink target stays in scope.
func TestThreadForkAcceptsRolloutUnderSymlinkedSessionsRootLikeRust(t *testing.T) {
	home := t.TempDir()
	externalSessions := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(externalSessions, 0o755); err != nil {
		t.Fatal(err)
	}
	linkSessionsDirForTest(t, externalSessions, filepath.Join(home, "sessions"))

	h := &attachedThreadHarness{home: home}
	h.open(t)
	t.Cleanup(h.close)
	parent := h.createPaginatedThread(t, "0198f001-0000-7000-8000-000000000421", t.TempDir())

	response := h.router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID: string(parent.ID),
	}))
	if response.Error != nil {
		t.Fatalf("thread/fork error = %+v", response.Error)
	}
	thread := response.Result.(*ThreadForkResponse).Thread
	if thread == nil || thread.ForkedFromID == nil || *thread.ForkedFromID != string(parent.ID) {
		t.Fatalf("fork thread = %+v, want forkedFromId %s", thread, parent.ID)
	}
}

// linkSessionsDirForTest creates a directory link without requiring Windows
// symlink privileges: junctions resolve like symlinks through the platform
// canonicalizer used by the managed-root check.
func linkSessionsDirForTest(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err == nil {
			return
		}
		t.Skipf("directory links are unavailable: %v: %s", err, strings.TrimSpace(string(output)))
	}
	t.Skip("directory symlinks are unavailable")
}
