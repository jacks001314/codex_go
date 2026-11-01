package execserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/shell"
	"codex_go/utils"
)

// Rust caps the captured environment on its own and adds it to the in-memory
// replay budget: a state that fits 512 KiB alone but not together with the
// environment must go through file replay, and an environment over the cap
// fails the capture.
func TestShellSnapshotEnvironmentSharesReplayBudgetLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params := &ExecParams{
		ProcessID: "snapshot-env-budget",
		Argv:      []string{"/bin/bash", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-env-budget",
			Shell:   ShellInfo{Name: "bash", Path: "/bin/bash"},
		},
		Env: map[string]string{"PATH": "/bin"},
	}
	state := "export VALUE=\"" + strings.Repeat("v", 20*1024) + "\"\n"
	environment := "BIG=" + strings.Repeat("x", 500*1024) + "\x00"
	stream := []byte("# Snapshot file\n" + state + "\x00\x00\x00" + environment)
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return stream, nil
	}
	entry, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil, nil)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if entry == nil || !entry.fileSource {
		t.Fatalf("state plus environment over the replay budget did not materialize a file: %#v", entry)
	}
	manager.close()

	oversized := newShellSnapshotManager()
	oversized.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return []byte("# Snapshot file\n\x00\x00\x00HUGE=" + strings.Repeat("x", 520*1024) + "\x00"), nil
	}
	overParams := *params
	overParams.ShellSnapshot = &ShellSnapshotRequest{ScopeID: "attachment-env-oversized", Shell: ShellInfo{Name: "bash", Path: "/bin/bash"}}
	if _, err := oversized.snapshotEntry(context.Background(), &overParams, shell.ShellBash, overParams.Env, nil, nil); err == nil {
		t.Fatal("an environment over 512 KiB was accepted")
	}
	oversized.close()
}
