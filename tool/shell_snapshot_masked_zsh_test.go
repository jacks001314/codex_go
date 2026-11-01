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

// TestSnapshotBuilderMasksRealZshCaptureLikeRust is the zsh half of the
// protected capture differential: a real zsh startup file defines an alias that
// carries the broker value, the capture is masked before the file is written,
// the replay sources cleanly, and the file never carries the real value.
func TestSnapshotBuilderMasksRealZshCaptureLikeRust(t *testing.T) {
	zsh := hostZshPath()
	if zsh == "" {
		t.Skip("no zsh on this host")
	}
	zdotdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdotdir, ".zshenv"), []byte("alias probe_alias='echo sk-real-secret'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{
		CodexHome: t.TempDir(),
		SessionID: "session-masked-zsh",
		Runner:    realZshSnapshotRunner(zsh, zdotdir),
	})
	t.Cleanup(builder.Close)
	snapshot, reason := builder.Snapshot(context.Background(), protectedZshSnapshotRequest(zsh, "sk-real-secret"))
	if snapshot == nil || reason != "" {
		t.Fatalf("Snapshot() = %v/%q, want a captured snapshot", snapshot, reason)
	}
	written, err := os.ReadFile(snapshot.Path())
	if err != nil {
		t.Fatalf("read snapshot error = %v", err)
	}
	if strings.Contains(string(written), "sk-real-secret") {
		t.Fatalf("zsh snapshot kept the real credential:\n%s", written)
	}
	if !strings.Contains(string(written), "sk-dummy-value") {
		t.Fatalf("zsh snapshot misses the brokered dummy:\n%s", written)
	}
}

// TestSnapshotBuilderRejectsRealZshReassembledCredentialLikeRust covers a zsh
// alias whose value zsh splits across quoting, so masking cannot see the
// contiguous credential and only the decoded literal check refuses it.
func TestSnapshotBuilderRejectsRealZshReassembledCredentialLikeRust(t *testing.T) {
	zsh := hostZshPath()
	if zsh == "" {
		t.Skip("no zsh on this host")
	}
	zdotdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdotdir, ".zshenv"), []byte("alias probe_alias=\"printf %s sk-real'secret\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	builder := NewSnapshotBuilder(SnapshotBuilderOptions{
		CodexHome: t.TempDir(),
		SessionID: "session-reassembled-zsh",
		Runner:    realZshSnapshotRunner(zsh, zdotdir),
	})
	t.Cleanup(builder.Close)
	snapshot, reason := builder.Snapshot(context.Background(), protectedZshSnapshotRequest(zsh, "sk-real'secret"))
	if snapshot != nil || reason != SnapshotReasonWriteFailed {
		t.Fatalf("Snapshot() = %v/%q, want a write failure", snapshot, reason)
	}
}

func protectedZshSnapshotRequest(zsh string, real string) SnapshotCaptureRequest {
	return SnapshotCaptureRequest{
		ShellType:         ShellZsh,
		ShellPath:         zsh,
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

// realZshSnapshotRunner runs capture and validation with the host zsh and
// injects ZDOTDIR so a non-interactive capture sees the startup file the user
// would have.
func realZshSnapshotRunner(zsh string, zdotdir string) SnapshotCaptureRunner {
	return func(ctx context.Context, command []string, cwd string, env map[string]string, _ *sandbox.PermissionProfile, _ string) ([]byte, error) {
		args := append([]string(nil), command[1:]...)
		// MSYS2 zsh opens a drive path only in POSIX form; the replay sources
		// the snapshot by its host path.
		if last := len(args) - 1; last >= 0 && strings.HasPrefix(args[last], "set -e; . \"") {
			args[last] = strings.ReplaceAll(args[last], "\\", "/")
		}
		cmd := exec.CommandContext(ctx, zsh, args...)
		cmd.Dir = cwd
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "ZDOTDIR=" + zdotdir}
		for key, value := range env {
			if key == "PATH" {
				continue
			}
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		return cmd.Output()
	}
}

// hostZshPath finds a host zsh for the differential; the tests skip without
// one.
func hostZshPath() string {
	if path, err := exec.LookPath("zsh"); err == nil {
		return path
	}
	for _, candidate := range []string{
		`C:\msys64\usr\bin\zsh.exe`,
		`C:\Program Files\Git\usr\bin\zsh.exe`,
		"/bin/zsh",
		"/usr/bin/zsh",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
