package appserver

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/session"
)

// TestThreadShellCommandLaunchUsesTheSessionShellAndSnapshotLikeRust covers Rust's
// tasks/user_shell.rs preparation: a user-typed command runs under the session's
// own shell, replays the session's snapshot so the user's aliases and functions
// apply, and keeps Codex's PATH entries afterwards.
func TestThreadShellCommandLaunchUsesTheSessionShellAndSnapshotLikeRust(t *testing.T) {
	stubShellSnapshotRunner(t)
	router, threadID, cwd := newShellSnapshotRouter(t, "sandbox_mode = \"workspace-write\"\n")
	t.Cleanup(func() { _ = router.Close() })
	// A host shell stands in for the detected one the CLI passes in production.
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, cwd)

	run := &threadShellCommandRun{ThreadID: threadID, Command: "echo hi", CWD: cwd}
	argv, env := router.threadShellCommandLaunch(context.Background(), run)
	if len(argv) != 3 {
		t.Fatalf("argv = %#v", argv)
	}
	if argv[0] != "/bin/bash" {
		t.Fatalf("argv shell = %#v, want the session shell", argv[0])
	}
	if runtime.GOOS == "windows" {
		// The snapshot wrapper is POSIX-only (Rust's cfg!(windows) guard), so a
		// Windows host runs the session shell directly.
		if argv[1] != "-lc" || argv[2] != "echo hi" {
			t.Fatalf("argv = %#v", argv)
		}
	} else {
		if argv[1] != "-c" || !strings.Contains(argv[2], "if . '") ||
			!strings.Contains(argv[2], "exec '/bin/bash' -c 'echo hi'") {
			t.Fatalf("argv does not replay the snapshot:\n%s", argv[2])
		}
	}
	// Either way the launch asked the session for its snapshot: the capture ran
	// and its file is under the session's codex home.
	snapshots, err := os.ReadDir(filepath.Join(router.codexHomeForRollout(), "shell_snapshots"))
	if err != nil || len(snapshots) == 0 {
		t.Fatalf("no session snapshot was captured: %#v/%v", snapshots, err)
	}
	if !envListContains(env, "CODEX_THREAD_ID="+threadID) {
		t.Fatalf("command env misses the thread id: %#v", env)
	}
	if !envListContainsPrefix(env, "PATH=") {
		t.Fatalf("command env misses PATH: %#v", env)
	}
}

// A thread without a resolvable shell keeps the previous plain `sh -lc` launch
// rather than failing the command the user typed.
func TestThreadShellCommandLaunchFallsBackWithoutAShell(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.services.Environment = NewEnvironmentManager(EnvironmentShellInfo{}, "")
	argv, env := router.threadShellCommandLaunch(context.Background(), &threadShellCommandRun{Command: "echo hi"})
	if env != nil {
		t.Fatalf("command env = %#v, want the inherited environment", env)
	}
	if len(argv) != 3 || argv[2] != "echo hi" {
		t.Fatalf("argv = %#v", argv)
	}
	if argv[0] == "" || argv[1] == "" {
		t.Fatalf("argv = %#v", argv)
	}
}

func envListContains(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func envListContainsPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}
