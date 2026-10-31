package appserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/tool"
)

// Rust #42137 `Session::prewarm_shell_snapshots`: after a turn's tools and
// permissions are fixed, the local environment's v2 snapshot is captured in the
// background so the first exec_command does not pay for it. The prewarm is
// skipped for an untrusted project, UnlessTrusted approval, managed networking
// and turns without exec_command.
func TestShellSnapshotV2PrewarmGatesLikeRust(t *testing.T) {
	previousCapability := localShellSnapshotV2Supported
	localShellSnapshotV2Supported = func() bool { return true }
	t.Cleanup(func() { localShellSnapshotV2Supported = previousCapability })
	previousPrewarm := shellSnapshotV2Prewarm
	t.Cleanup(func() { shellSnapshotV2Prewarm = previousPrewarm })

	cases := []struct {
		name        string
		features    string
		wantPrewarm bool
	}{
		{"eligible turn", "shell_snapshot = false\nshell_snapshot_v2 = true\n", true},
		{"untrusted project", "shell_snapshot = false\nshell_snapshot_v2 = true\n", false},
		{"unless-trusted approval", "shell_snapshot = false\nshell_snapshot_v2 = true\n", false},
		{"exec_command disabled", "shell_snapshot = false\nshell_snapshot_v2 = true\nunified_exec = false\n", false},
		{"v2 disabled", "shell_snapshot = false\nshell_snapshot_v2 = false\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, threadID, cwd := newShellSnapshotRouter(t, "")
			t.Cleanup(func() { _ = router.Close() })
			router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)

			body := "sandbox_mode = \"workspace-write\"\n"
			if tc.name == "unless-trusted approval" {
				body += "approval_policy = \"unless-trusted\"\n"
			}
			body += "[features]\n" + tc.features
			trust := "trusted"
			if tc.name == "untrusted project" {
				trust = "untrusted"
			}
			body += fmt.Sprintf("[projects.%q]\ntrust_level = %q\n", filepath.Clean(cwd), trust)
			if err := os.WriteFile(config.ConfigPath(router.codexHomeForRollout()), []byte(body), 0o600); err != nil {
				t.Fatalf("write config error = %v", err)
			}

			requests := make(chan *tool.ShellRequest, 4)
			shellSnapshotV2Prewarm = func(_ context.Context, request *tool.ShellRequest) error {
				requests <- request
				return nil
			}

			cfg := router.effectiveWriteStdinConfig(threadID)
			if cfg == nil {
				t.Fatal("effectiveWriteStdinConfig() = nil")
			}
			provider := router.shellSnapshotProviderForTurn(threadID, cfg)
			if tc.name != "v2 disabled" && provider == nil {
				t.Fatal("a session with shell_snapshot_v2 has no provider")
			}

			if !tc.wantPrewarm {
				select {
				case request := <-requests:
					t.Fatalf("prewarm = %#v, want none", request.LocalShellSnapshot)
				case <-time.After(200 * time.Millisecond):
				}
				return
			}

			select {
			case request := <-requests:
				if request == nil || request.LocalShellSnapshot == nil {
					t.Fatalf("prewarm request = %#v", request)
				}
				if request.LocalShellSnapshot.ScopeID != threadID+":local" || request.LocalShellSnapshot.Shell.Path != "/bin/bash" {
					t.Fatalf("prewarm snapshot = %#v", request.LocalShellSnapshot)
				}
				if !sameDirectory(request.CWD, cwd) || request.ThreadID != threadID {
					t.Fatalf("prewarm cwd/thread = %q/%q, want %q/%q", request.CWD, request.ThreadID, cwd, threadID)
				}
				if !request.Shell.IsPosixLogin() {
					t.Fatalf("prewarm shell = %#v, want a POSIX login invocation", request.Shell)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the eligible turn did not prewarm its shell snapshot")
			}

			// Rust keeps one prewarm handle per session, so a later turn's
			// provider creation must not start another speculative launch.
			if provider := router.shellSnapshotProviderForTurn(threadID, cfg); provider == nil {
				t.Fatal("the provider disappeared after the prewarm")
			}
			select {
			case request := <-requests:
				t.Fatalf("second prewarm = %#v, want one per session", request.LocalShellSnapshot)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}

// The prewarm and the launch must agree on the scope, so a warmed capture is
// available to the launch's provider answer.
func TestShellSnapshotV2PrewarmAndLaunchShareTheScopeLikeRust(t *testing.T) {
	previousCapability := localShellSnapshotV2Supported
	localShellSnapshotV2Supported = func() bool { return true }
	t.Cleanup(func() { localShellSnapshotV2Supported = previousCapability })
	previousPrewarm := shellSnapshotV2Prewarm
	prewarmed := make(chan *tool.ShellRequest, 1)
	shellSnapshotV2Prewarm = func(_ context.Context, request *tool.ShellRequest) error {
		prewarmed <- request
		return nil
	}
	t.Cleanup(func() { shellSnapshotV2Prewarm = previousPrewarm })

	router, threadID, cwd := newShellSnapshotRouter(t, "")
	t.Cleanup(func() { _ = router.Close() })
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)
	body := "sandbox_mode = \"workspace-write\"\n[features]\nshell_snapshot = false\nshell_snapshot_v2 = true\n" +
		fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", filepath.Clean(cwd))
	if err := os.WriteFile(config.ConfigPath(router.codexHomeForRollout()), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cfg := router.effectiveWriteStdinConfig(threadID)
	provider := router.shellSnapshotProviderForTurn(threadID, cfg)
	if provider == nil {
		t.Fatal("no provider")
	}
	var prewarm *tool.ShellRequest
	select {
	case prewarm = <-prewarmed:
	case <-time.After(2 * time.Second):
		t.Fatal("no prewarm")
	}
	launch := provider(context.Background(), tool.SnapshotProviderRequest{
		ShellType:       tool.ShellBash,
		ShellPath:       "/bin/bash",
		CWD:             cwd,
		AllowLoginShell: true,
	})
	if launch.Local == nil || prewarm.LocalShellSnapshot == nil {
		t.Fatalf("launch = %#v, prewarm = %#v", launch.Local, prewarm.LocalShellSnapshot)
	}
	if launch.Local.ScopeID != prewarm.LocalShellSnapshot.ScopeID || launch.Local.Shell != prewarm.LocalShellSnapshot.Shell {
		t.Fatalf("launch scope %#v != prewarm scope %#v", launch.Local, prewarm.LocalShellSnapshot)
	}
	if !strings.HasSuffix(launch.Local.ScopeID, ":local") {
		t.Fatalf("scope = %q, want thread:local", launch.Local.ScopeID)
	}
}
