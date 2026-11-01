//go:build linux

package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"codex_go/sandbox/linuxsandbox"
	"codex_go/shell"
	"codex_go/utils"
)

// TestMain lets this package's test binary act as the production
// `codex-linux-sandbox` helper for the sandbox end-to-end tests below. Go's
// test driver always passes `-test.*` flags, while the helper argv starts with
// `--sandbox-policy-cwd`, `--apply-seccomp-then-exec`, or a proxy bridge flag,
// so the two invocations cannot be confused.
func TestMain(m *testing.M) {
	if args := os.Args[1:]; linuxSandboxHelperInvocation(args) {
		os.Exit(linuxsandbox.RunHelper(args, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func linuxSandboxHelperInvocation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--sandbox-policy-cwd", "--apply-seccomp-then-exec", "--proxy-host-bridge", "--proxy-local-bridge":
		return true
	default:
		return false
	}
}

// installShellSnapshotSandboxHelper puts a `codex-linux-sandbox` alias for this
// test binary first on PATH, the same shape the installed CLI exposes.
func installShellSnapshotSandboxHelper(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, linuxsandbox.LinuxSandboxArg0)); err != nil {
		t.Fatalf("install sandbox helper alias: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// Rust #48078 probes descriptor replay inside the capture sandbox itself, so an
// unavailable /dev/fd must select environment replay and an available path must
// select descriptor replay without consulting host-only state.
//
// The denied case withholds the carrier descriptor instead of adding a deny
// `/dev/fd` entry: bubblewrap's `--dev /dev` recreates /dev/fd as a symlink to
// /proc/self/fd, and bwrap 0.11 cannot mount any mask over that symlink
// (tmpfs and bind mounts both fail with ENOENT). Rust emits the same mask, so
// the two implementations share that bwrap limitation. Withholding fd 3 is the
// syscall-visible effect the probe observes: opening /dev/fd/3 fails.
func TestShellSnapshotCaptureProbesExactSandboxLikeRust(t *testing.T) {
	bash := hostSnapshotBash()
	if bash == "" {
		t.Skip("bash is unavailable on this host")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap is unavailable: %v", err)
	}
	helperDir := installShellSnapshotSandboxHelper(t)
	for _, testCase := range []struct {
		name            string
		withholdCarrier bool
		wantDescriptor  bool
	}{
		{name: "descriptor-allowed", wantDescriptor: true},
		{name: "descriptor-unavailable", withholdCarrier: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			cwd := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(home, ".bash_profile"),
				[]byte("profile_helper() { printf 'restored:%s' \"$1\"; }\nexport SNAPSHOT_VALUE=restored\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			cwdURI, err := utils.FromHostNativePath(cwd)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"HOME": home, "PATH": helperDir + ":/usr/bin:/bin"}
			params := &ExecParams{
				ProcessID: "snapshot-sandbox-probe",
				Argv:      []string{bash, "-lc", "profile_helper probe"},
				CWD:       cwdURI.String(),
				Env:       env,
				Sandbox:   shellSnapshotTestSandboxJSON(t, cwd),
				ShellSnapshot: &ShellSnapshotRequest{
					ScopeID: "snapshot-sandbox-probe-" + testCase.name,
					Shell:   ShellInfo{Name: "bash", Path: bash},
				},
			}
			manager := newShellSnapshotManager()
			t.Cleanup(manager.close)
			manager.capture = func(ctx context.Context, command []string, cwd string, env map[string]string) ([]byte, error) {
				if len(command) == 0 || filepath.Base(command[0]) != linuxsandbox.LinuxSandboxArg0 {
					t.Fatalf("capture did not run under the launch sandbox: %#v", command)
				}
				process := exec.CommandContext(ctx, command[0], command[1:]...)
				process.Dir = cwd
				process.Env = envPairs(env)
				if probe, ok := ctx.Value(shellSnapshotProbeKey{}).(*os.File); ok && probe != nil && !testCase.withholdCarrier {
					process.ExtraFiles = []*os.File{probe}
				}
				var stderr bytes.Buffer
				process.Stderr = &stderr
				output, err := process.Output()
				if err != nil {
					t.Logf("sandbox capture failed: %v\ncommand=%q\ncwd=%s\nstderr=%s", err, command, cwd, stderr.String())
				}
				return output, err
			}
			entry, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, env, nil, nil)
			if err != nil {
				t.Fatalf("capture under sandbox: %v", err)
			}
			if entry == nil || entry.state == "" {
				t.Fatalf("captured entry = %#v", entry)
			}
			if entry.fileSource != testCase.wantDescriptor {
				t.Fatalf("fileSource = %v, want %v", entry.fileSource, testCase.wantDescriptor)
			}
		})
	}
}

func shellSnapshotTestSandboxJSON(t *testing.T, cwd string) json.RawMessage {
	t.Helper()
	cwdURI, err := utils.FromHostNativePath(cwd)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := json.Marshal(map[string]any{
		"type": "managed",
		"file_system": map[string]any{"type": "restricted", "entries": []any{
			map[string]any{
				"path":   map[string]any{"type": "special", "value": map[string]any{"kind": "root"}},
				"access": "read",
			},
			map[string]any{
				"path":   map[string]any{"type": "path", "path": cwdURI.String()},
				"access": "write",
			},
		}},
		"network": "restricted",
	})
	if err != nil {
		t.Fatal(err)
	}
	contextJSON, err := json.Marshal(FileSystemSandboxContext{
		Permissions:         profile,
		CWD:                 cwdURI.String(),
		WindowsSandboxLevel: "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	return contextJSON
}
