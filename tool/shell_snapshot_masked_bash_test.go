package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/network"
	"codex_go/sandbox"
)

// TestSnapshotBuilderMasksRealBashCaptureLikeRust runs the protected capture end
// to end through a real bash: the captured alias carries the real broker value,
// masking replaces it before the file is written, the replay still sources
// cleanly, and the file never carries the real value.
func TestSnapshotBuilderMasksRealBashCaptureLikeRust(t *testing.T) {
	bash := hostBashPath()
	if bash == "" {
		t.Skip("no bash on this host")
	}
	prelude := filepath.Join(t.TempDir(), "prelude.bash")
	if err := os.WriteFile(prelude, []byte("alias probe_alias='echo sk-real-secret'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{
		CodexHome: t.TempDir(),
		SessionID: "session-masked",
		Runner:    realBashSnapshotRunner(bash, prelude),
	})
	t.Cleanup(builder.Close)
	snapshot, reason := builder.Snapshot(context.Background(), protectedBashSnapshotRequest(bash, "sk-real-secret"))
	if snapshot == nil || reason != "" {
		t.Fatalf("Snapshot() = %v/%q, want a captured snapshot", snapshot, reason)
	}
	written, err := os.ReadFile(snapshot.Path())
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	if strings.Contains(string(written), "sk-real-secret") {
		t.Fatalf("snapshot kept the real credential:\n%s", written)
	}
	if !strings.Contains(string(written), "sk-dummy-value") {
		t.Fatalf("snapshot misses the brokered dummy:\n%s", written)
	}
}

// TestSnapshotBuilderRejectsRealBashReassembledCredentialLikeRust covers the
// same end-to-end path for a credential bash splits across alias quoting: the
// alias value "printf %s sk-real'secret" is captured as
// 'printf %s sk-real'\”secret', so masking cannot see it and only the decoded
// literal check refuses the snapshot.
func TestSnapshotBuilderRejectsRealBashReassembledCredentialLikeRust(t *testing.T) {
	bash := hostBashPath()
	if bash == "" {
		t.Skip("no bash on this host")
	}
	prelude := filepath.Join(t.TempDir(), "prelude.bash")
	body := "alias probe_alias=\"printf %s sk-real'secret\"\n"
	if err := os.WriteFile(prelude, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{
		CodexHome: t.TempDir(),
		SessionID: "session-reassembled",
		Runner:    realBashSnapshotRunner(bash, prelude),
	})
	t.Cleanup(builder.Close)
	snapshot, reason := builder.Snapshot(context.Background(), protectedBashSnapshotRequest(bash, "sk-real'secret"))
	if snapshot != nil || reason != SnapshotReasonWriteFailed {
		t.Fatalf("Snapshot() = %v/%q, want a write failure", snapshot, reason)
	}
}

// protectedBashSnapshotRequest builds a protected launch whose broker real
// value is the given secret and whose child-visible value is a dummy.
func protectedBashSnapshotRequest(bash string, real string) SnapshotCaptureRequest {
	return SnapshotCaptureRequest{
		ShellType:         ShellBash,
		ShellPath:         bash,
		CWD:               os.TempDir(),
		AllowLoginShell:   false,
		Protected:         true,
		Env:               map[string]string{"PATH": os.Getenv("PATH")},
		SourceEnv:         map[string]string{"PATH": os.Getenv("PATH")},
		CredentialPairs:   []network.CredentialMaskPair{{Key: "SNAPSHOT_PROBE_TOKEN", Real: real, Dummy: "sk-dummy-value"}},
		CredentialEnvKeys: []string{"SNAPSHOT_PROBE_TOKEN"},
		CredentialTextUnsafe: func(text string) bool {
			return strings.Contains(text, real)
		},
	}
}

// realBashSnapshotRunner runs capture and validation commands with the host
// bash, injecting the prelude through BASH_ENV so a non-login capture sees the
// aliases a user's startup file would define.
func realBashSnapshotRunner(bash string, prelude string) SnapshotCaptureRunner {
	return func(ctx context.Context, command []string, cwd string, env map[string]string, _ *sandbox.PermissionProfile, _ string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bash, command[1:]...)
		cmd.Dir = cwd
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "BASH_ENV=" + prelude}
		for key, value := range env {
			if key == "PATH" {
				continue
			}
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		return cmd.Output()
	}
}
