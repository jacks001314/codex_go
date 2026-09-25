package tool

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestRuntimePathPrependsMatchRust pins Rust's prepend_path_entry and the
// recorded replay order: the entry goes first, an existing copy moves to the
// front, and the entries replay so the last one prepended ends up first again.
func TestRuntimePathPrependsMatchRust(t *testing.T) {
	prepends := &RuntimePathPrepends{}
	separator := string(filepath.ListSeparator)
	existing := "/usr/bin" + separator + "/bin"
	env := map[string]string{"PATH": existing}
	prepends.Prepend(env, "/opt/codex-codex-path")
	if env["PATH"] != "/opt/codex-codex-path"+separator+existing {
		t.Fatalf("PATH = %q", env["PATH"])
	}
	prepends.Prepend(env, "/opt/zsh/bin")
	if want := "/opt/zsh/bin" + separator + "/opt/codex-codex-path" + separator + existing; env["PATH"] != want {
		t.Fatalf("PATH = %q, want %q", env["PATH"], want)
	}
	if got := prepends.Entries(); len(got) != 2 || got[0] != "/opt/codex-codex-path" || got[1] != "/opt/zsh/bin" {
		t.Fatalf("entries = %#v", got)
	}
	// Replaying the entries in order restores the same PATH.
	replayed := map[string]string{"PATH": existing}
	applyRuntimePathPrepends(replayed, prepends.Entries())
	if replayed["PATH"] != env["PATH"] {
		t.Fatalf("replayed PATH = %q, want %q", replayed["PATH"], env["PATH"])
	}
	// Moving an existing entry to the front drops the older occurrence.
	prepends.Prepend(env, "/usr/bin")
	if strings.Count(env["PATH"], "/usr/bin") != 1 || !strings.HasPrefix(env["PATH"], "/usr/bin"+separator) {
		t.Fatalf("PATH = %q", env["PATH"])
	}
	// An empty entry changes nothing; an empty PATH becomes the entry.
	prepends.Prepend(env, "")
	empty := map[string]string{}
	prepends.Prepend(empty, "/only")
	if empty["PATH"] != "/only" {
		t.Fatalf("PATH = %q", empty["PATH"])
	}
}

// TestRuntimePathPrependsShellExportsMatchRust pins Rust's
// shell_exports_after_snapshot: one export per entry, and nothing at all when the
// user overrides PATH explicitly.
func TestRuntimePathPrependsShellExportsMatchRust(t *testing.T) {
	prepends := &RuntimePathPrepends{entries: []string{"/opt/codex-path", "/opt/zsh's bin"}}
	exports := prepends.ShellExportsAfterSnapshot(nil)
	want := "if [ -n \"${PATH:-}\" ]; then export PATH='/opt/codex-path':\"$PATH\"; else export PATH='/opt/codex-path'; fi\n" +
		"if [ -n \"${PATH:-}\" ]; then export PATH='/opt/zsh'\"'\"'s bin':\"$PATH\"; else export PATH='/opt/zsh'\"'\"'s bin'; fi"
	if exports != want {
		t.Fatalf("exports = %q, want %q", exports, want)
	}
	if got := prepends.ShellExportsAfterSnapshot(map[string]string{"PATH": "/custom"}); got != "" {
		t.Fatalf("exports with an explicit PATH = %q, want none", got)
	}
	if got := (&RuntimePathPrepends{}).ShellExportsAfterSnapshot(nil); got != "" {
		t.Fatalf("exports without entries = %q, want none", got)
	}
}

// TestRuntimePathEntriesForLaunchIncludeThePackageAndZshForkDirs covers Rust's
// two prepend sources: the packaged `codex-path` directory and, for a zsh-fork
// launch, the forked shell's directory.
func TestRuntimePathEntriesForLaunchIncludeThePackageAndZshForkDirs(t *testing.T) {
	previous := installPackagePathDir
	installPackagePathDir = func() string { return "/opt/codex/bin/codex-path" }
	t.Cleanup(func() { installPackagePathDir = previous })
	entries := RuntimePathEntriesForLaunch("/opt/codex/zsh/bin/zsh")
	if len(entries) != 2 || entries[0] != "/opt/codex/bin/codex-path" || entries[1] != "/opt/codex/zsh/bin" {
		t.Fatalf("entries = %#v", entries)
	}
	if got := RuntimePathEntriesForLaunch(""); len(got) != 1 || got[0] != "/opt/codex/bin/codex-path" {
		t.Fatalf("entries without a zsh fork = %#v", got)
	}
	installPackagePathDir = func() string { return "" }
	if got := RuntimePathEntriesForLaunch("zsh"); len(got) != 0 {
		t.Fatalf("entries with a bare shell name = %#v", got)
	}
}

// TestMaybeWrapShellLCWithSnapshotReplaysRuntimePathsLikeRust pins the wrapper
// half: the runtime's own PATH entries are re-exported after the snapshot is
// sourced, unless the user overrode PATH explicitly.
func TestMaybeWrapShellLCWithSnapshotReplaysRuntimePathsLikeRust(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	rewritten := MaybeWrapShellLCWithSnapshot(
		[]string{"/bin/bash", "-lc", "echo hello"},
		&Shell{Type: ShellBash, Path: "/bin/bash"},
		snapshotPath, nil, nil, []string{"/opt/codex-path"},
	)
	if len(rewritten) != 3 {
		t.Fatalf("rewritten = %#v", rewritten)
	}
	sourceIndex := strings.Index(rewritten[2], "if . '"+snapshotPath+"'")
	exportIndex := strings.Index(rewritten[2], "export PATH='/opt/codex-path':\"$PATH\"")
	execIndex := strings.Index(rewritten[2], "exec '/bin/bash' -c 'echo hello'")
	if sourceIndex < 0 || exportIndex < 0 || execIndex < 0 || !(sourceIndex < exportIndex && exportIndex < execIndex) {
		t.Fatalf("wrapper = \n%s", rewritten[2])
	}

	overridden := MaybeWrapShellLCWithSnapshot(
		[]string{"/bin/bash", "-lc", "echo hello"},
		&Shell{Type: ShellBash, Path: "/bin/bash"},
		snapshotPath, map[string]string{"PATH": "/custom"}, nil, []string{"/opt/codex-path"},
	)
	if strings.Contains(overridden[2], "export PATH='/opt/codex-path'") {
		t.Fatalf("an explicit PATH override was replaced:\n%s", overridden[2])
	}
}

// TestShellExecutorKeepsRuntimePathsOnLaunchLikeRust covers the launch wiring:
// a Unix launch records the runtime PATH entries, the shell runner applies them
// to the command environment, and a remote launch leaves them to its host.
func TestShellExecutorKeepsRuntimePathsOnLaunchLikeRust(t *testing.T) {
	previousSupported := runtimePathPrependsSupported
	previousPathDir := installPackagePathDir
	runtimePathPrependsSupported = true
	installPackagePathDir = func() string { return "/opt/codex-path" }
	t.Cleanup(func() {
		runtimePathPrependsSupported = previousSupported
		installPackagePathDir = previousPathDir
	})
	workspaceWrite := sandbox.WorkspaceWritePermissionProfile()
	var launched *ShellRequest
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: pluginMetricsShellRunner{onRun: func(req *ShellRequest) { launched = req }},
		Shell:  &Shell{Type: ShellBash, Path: "/bin/bash"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:      sandbox.ApprovalOnRequest,
			CWD:                 t.TempDir(),
			DefaultTimeoutMS:    5000,
			PermissionProfile:   &workspaceWrite,
			PermissionProfileID: "resolved",
		},
	})
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-runtime-paths",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if launched == nil {
		t.Fatal("the shell runner did not see the launch request")
	}
	if len(launched.RuntimePathPrepends) != 1 || launched.RuntimePathPrepends[0] != "/opt/codex-path" {
		t.Fatalf("RuntimePathPrepends = %#v", launched.RuntimePathPrepends)
	}
	// The runner puts the entries on the command environment's PATH.
	launched.Env = map[string]string{"PATH": "/usr/bin"}
	env := shellRequestEnv(launched)
	if !strings.HasPrefix(env["PATH"], "/opt/codex-path"+string(filepath.ListSeparator)) {
		t.Fatalf("command PATH = %q", env["PATH"])
	}
}
