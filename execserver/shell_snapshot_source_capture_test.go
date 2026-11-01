package execserver

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust's exec-server captures with snapshot_source_capture_script, which emits
// options and aliases inside one brace group, while POSIX sh keeps the
// evaluating form. The capture command the executor runs must follow that.
func TestShellSnapshotCaptureUsesSourceScriptLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	for _, testCase := range []struct {
		name      string
		grouped   bool
		shellType string
	}{
		{name: "bash", grouped: true, shellType: "bash"},
		{name: "zsh", grouped: true, shellType: "zsh"},
		{name: "sh", grouped: false, shellType: "sh"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var script string
			manager := newShellSnapshotManager()
			manager.capture = func(_ context.Context, command []string, _ string, _ map[string]string) ([]byte, error) {
				script = command[2]
				return []byte("# Snapshot file\n\x00\x00\x00"), nil
			}
			cwd, err := utils.FromHostNativePath(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			params := &ExecParams{
				ProcessID: "snapshot-source-process",
				Argv:      []string{"/bin/" + testCase.shellType, "-lc", "echo ok"},
				CWD:       cwd.String(),
				ShellSnapshot: &ShellSnapshotRequest{
					ScopeID: "attachment-source",
					Shell:   ShellInfo{Name: testCase.shellType, Path: "/bin/" + testCase.shellType},
				},
				Env: map[string]string{"PATH": "/bin"},
			}
			if _, err := manager.rewrite(context.Background(), params, params.Env, nil, nil); err != nil {
				t.Fatal(err)
			}
			if grouped := strings.Contains(script, "case '' in '') ;; esac"); grouped != testCase.grouped {
				t.Fatalf("%s capture grouped=%v, want %v:\n%s", testCase.name, grouped, testCase.grouped, script)
			}
			if testCase.grouped && !strings.Contains(script, "printf '{\\n'") {
				t.Fatalf("%s capture misses the brace group:\n%s", testCase.name, script)
			}
		})
	}
}

// The grouped capture stays replayable end to end: a real shell captures its
// state, the executor parses and renders it, and the rewritten launch evals the
// snapshot before running the command.
func TestShellSnapshotSourceCaptureReplaysRealShellLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	for _, shellCase := range []struct {
		name string
		path string
	}{
		{name: "bash", path: hostSnapshotBash()},
		{name: "zsh", path: hostSnapshotZsh()},
	} {
		if shellCase.path == "" {
			t.Run(shellCase.name, func(t *testing.T) { t.Skip("no " + shellCase.name + " on this host") })
			continue
		}
		t.Run(shellCase.name, func(t *testing.T) {
			manager := newShellSnapshotManager()
			manager.capture = func(ctx context.Context, command []string, cwd string, env map[string]string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, command[0], command[1:]...)
				cmd.Dir = cwd
				cmd.Env = envPairs(env)
				return cmd.Output()
			}
			cwd, err := utils.FromHostNativePath(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			params := &ExecParams{
				ProcessID: "snapshot-source-process",
				Argv:      []string{shellCase.path, "-lc", "echo snapshot-ok"},
				CWD:       cwd.String(),
				ShellSnapshot: &ShellSnapshotRequest{
					ScopeID: "attachment-source-real",
					Shell:   ShellInfo{Name: shellCase.name, Path: shellCase.path},
				},
				Env: map[string]string{"PATH": os.Getenv("PATH")},
			}
			rewritten, err := manager.rewrite(context.Background(), params, params.Env, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(rewritten.Argv[0], rewritten.Argv[1:]...)
			cmd.Env = envPairs(rewritten.Env)
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("replay failed: %v\n%s", err, rewritten.Argv[2])
			}
			if !strings.Contains(string(output), "snapshot-ok") {
				t.Fatalf("replay output = %q", output)
			}
			manager.close()
		})
	}
}

func hostSnapshotZsh() string {
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

func hostSnapshotBash() string {
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	for _, candidate := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		"/bin/bash",
		"/usr/bin/bash",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
