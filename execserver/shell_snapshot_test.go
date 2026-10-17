package execserver

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/utils"
)

func TestShellSnapshotRequestRoundTripAndCapability(t *testing.T) {
	params := ExecParams{
		ProcessID: "snapshot-process",
		Argv:      []string{"/bin/zsh", "-lc", "echo ok"},
		CWD:       "file:///repo",
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "zsh", Path: "/bin/zsh"},
		},
		Env: map[string]string{},
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"shellSnapshot":{"scopeId":"attachment-1"`) {
		t.Fatalf("shell snapshot wire form = %s", encoded)
	}
	var decoded ExecParams
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ShellSnapshot == nil || decoded.ShellSnapshot.ScopeID != "attachment-1" {
		t.Fatalf("decoded shell snapshot = %#v", decoded.ShellSnapshot)
	}
	if got := localEnvironmentInfo().Capabilities.ShellSnapshotV2; got != (runtime.GOOS != "windows") {
		t.Fatalf("shellSnapshotV2 = %t", got)
	}
}

func TestShellSnapshotManagerRewritesAndCachesLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	var calls atomic.Int64
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		calls.Add(1)
		return []byte("# Snapshot file\n# Functions\n\x00\x00\x00"), nil
	}
	cwd, err := utils.FromHostNativePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params := &ExecParams{
		ProcessID: "snapshot-process",
		Argv:      []string{"/bin/zsh", "-lc", "echo ok"},
		CWD:       cwd.String(),
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "zsh", Path: "/bin/zsh"},
		},
		Env: map[string]string{"PATH": "/bin"},
	}
	rewritten, err := manager.rewrite(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	script := rewritten.Argv[2]
	if !strings.HasPrefix(script, ". '") || !strings.Contains(script, "|| exit 1\n") || !strings.HasSuffix(script, "echo ok") {
		t.Fatalf("rewritten script = %q", script)
	}
	again, err := manager.rewrite(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || again.Argv[2] != script {
		t.Fatalf("snapshot cache calls=%d script=%q", calls.Load(), again.Argv[2])
	}
	path := strings.TrimSpace(strings.Split(script, "|| exit 1")[0])
	path = strings.TrimSpace(strings.TrimPrefix(path, ". '"))
	path = strings.TrimSpace(strings.TrimSuffix(path, "'"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file = %q: %v", path, err)
	}
	manager.close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot file survived close: %v", err)
	}
}
