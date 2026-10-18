package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/network"
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
	if got := localEnvironmentInfo().Capabilities.ShellSnapshotV2; got {
		t.Fatal("local exec-server must not advertise shellSnapshotV2")
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
		return []byte("# Snapshot file\nexport TOKEN=\"sk-real-secret\"\n\x00\x00\x00"), nil
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
		Env: map[string]string{
			"PATH": "/bin", "HTTP_PROXY": "http://127.0.0.1:8080",
			network.CredentialBrokerActiveEnvKey: "1", "TOKEN": "sk-dummy-value",
			network.BrokeredCredentialsEnvKey: `[["TOKEN","sk-dummy-value"]]`,
		},
	}
	pairs := []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	rewritten, err := manager.rewrite(context.Background(), params, params.Env, pairs)
	if err != nil {
		t.Fatal(err)
	}
	script := rewritten.Argv[2]
	if !strings.HasPrefix(script, ". '") || !strings.Contains(script, "|| exit 1\n") || !strings.HasSuffix(script, "echo ok") {
		t.Fatalf("rewritten script = %q", script)
	}
	if !strings.Contains(script, "\\builtin export HTTP_PROXY='http://127.0.0.1:8080'") ||
		!strings.Contains(script, "\\builtin export "+network.CredentialBrokerActiveEnvKey+"='1'") ||
		!strings.Contains(script, "\\builtin export TOKEN='sk-dummy-value'") {
		t.Fatalf("launch environment was not restored after snapshot: %q", script)
	}
	again, err := manager.rewrite(context.Background(), params, params.Env, pairs)
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
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "sk-real-secret") || !strings.Contains(string(contents), "sk-dummy-value") {
		t.Fatalf("snapshot credentials were not masked: %s", contents)
	}
	manager.close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("snapshot file survived close: %v", err)
	}
}

func TestShellSnapshotCaptureFailureRestoresCredentialsLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		return nil, errors.New("capture failed")
	}
	params := &ExecParams{
		ProcessID: "fallback-process",
		Argv:      []string{"/bin/zsh", "-lc", "echo ok"},
		CWD:       "file:///repo",
		ShellSnapshot: &ShellSnapshotRequest{
			ScopeID: "attachment-1",
			Shell:   ShellInfo{Name: "zsh", Path: "/bin/zsh"},
		},
		Env: map[string]string{"TOKEN": "sk-dummy-value"},
	}
	pairs := []network.CredentialMaskPair{{Real: "sk-real-secret", Dummy: "sk-dummy-value"}}
	rewritten, err := manager.rewrite(context.Background(), params, params.Env, pairs)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Env["TOKEN"] != "sk-real-secret" {
		t.Fatalf("fallback env = %#v", rewritten.Env)
	}
	if !strings.Contains(rewritten.Argv[2], "Credential masking is unavailable") || !strings.HasSuffix(rewritten.Argv[2], "echo ok") {
		t.Fatalf("fallback script = %q", rewritten.Argv[2])
	}
}
