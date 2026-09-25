package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/network"
)

// snapshotWrapFixture writes a snapshot file and enables the rewrite on the
// current host (the wrapper is POSIX-only, so a Windows host would otherwise
// leave every command alone).
func snapshotWrapFixture(t *testing.T) string {
	t.Helper()
	previous := snapshotWrapSupported
	snapshotWrapSupported = true
	t.Cleanup(func() { snapshotWrapSupported = previous })
	snapshotPath := filepath.Join(t.TempDir(), "shell_snapshots", "session.1.sh")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o700); err != nil {
		t.Fatalf("create snapshot dir error = %v", err)
	}
	if err := os.WriteFile(snapshotPath, []byte("# Snapshot file\n"), 0o600); err != nil {
		t.Fatalf("write snapshot error = %v", err)
	}
	return snapshotPath
}

// TestMaybeWrapShellLCWithSnapshotBootstrapsInSessionShellLikeRust mirrors Rust's
// `maybe_wrap_shell_lc_with_snapshot_bootstraps_in_user_shell`: the wrapper runs
// the session's own shell, sources the snapshot, and re-executes the caller's
// command in the shell the model asked for.
func TestMaybeWrapShellLCWithSnapshotBootstrapsInSessionShellLikeRust(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	sessionShell := &Shell{Type: ShellZsh, Path: "/bin/zsh"}
	command := []string{"/bin/bash", "-lc", "echo hello"}
	rewritten := MaybeWrapShellLCWithSnapshot(command, sessionShell, snapshotPath, nil, nil)
	if len(rewritten) != 3 {
		t.Fatalf("rewritten = %#v", rewritten)
	}
	if rewritten[0] != "/bin/zsh" || rewritten[1] != "-c" {
		t.Fatalf("rewritten head = %#v", rewritten[:2])
	}
	if !strings.Contains(rewritten[2], "if . '"+snapshotPath+"' >/dev/null 2>&1; then :; fi") {
		t.Fatalf("wrapper does not source the snapshot:\n%s", rewritten[2])
	}
	if !strings.Contains(rewritten[2], "exec '/bin/bash' -c 'echo hello'") {
		t.Fatalf("wrapper does not re-execute the caller's shell:\n%s", rewritten[2])
	}
	// With nothing to override, only the runtime-only keys are captured, so an
	// inactive value cannot resurface from the snapshot.
	const wantCaptures = `__CODEX_SNAPSHOT_OVERRIDE_SET_0="${CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS+x}"
__CODEX_SNAPSHOT_OVERRIDE_0="${CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_1="${CODEX_PERMISSION_PROFILE+x}"
__CODEX_SNAPSHOT_OVERRIDE_1="${CODEX_PERMISSION_PROFILE-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_2="${CODEX_PLUGIN_METRICS_OUTPUT+x}"
__CODEX_SNAPSHOT_OVERRIDE_2="${CODEX_PLUGIN_METRICS_OUTPUT-}"`
	if !strings.HasPrefix(rewritten[2], wantCaptures+"\n\n") {
		t.Fatalf("override captures = \n%s\nwant \n%s", rewritten[2], wantCaptures)
	}

	// Arguments after the script are carried into the re-executed command.
	withArguments := MaybeWrapShellLCWithSnapshot(
		[]string{"/bin/bash", "-lc", "echo hello", "one", "two'three"},
		sessionShell, snapshotPath, nil, nil,
	)
	if !strings.Contains(withArguments[2], `exec '/bin/bash' -c 'echo hello' 'one' 'two'"'"'three'`) {
		t.Fatalf("trailing arguments were not preserved:\n%s", withArguments[2])
	}
}

// TestMaybeWrapShellLCWithSnapshotEscapesSingleQuotesLikeRust mirrors Rust's
// `..._escapes_single_quotes`.
func TestMaybeWrapShellLCWithSnapshotEscapesSingleQuotesLikeRust(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	rewritten := MaybeWrapShellLCWithSnapshot(
		[]string{"/bin/bash", "-lc", "echo 'hello'"},
		&Shell{Type: ShellZsh, Path: "/bin/zsh"}, snapshotPath, nil, nil,
	)
	if !strings.Contains(rewritten[2], `exec '/bin/bash' -c 'echo '"'"'hello'"'"''`) {
		t.Fatalf("single quotes were not escaped:\n%s", rewritten[2])
	}
}

// TestMaybeWrapShellLCWithSnapshotRestoresRuntimeEnvLikeRust pins the override
// capture/restore pair: the launch's own variables and the policy overrides are
// saved before the snapshot is sourced and put back afterwards, and a variable
// the launch does not define is unset again instead of surviving from the
// snapshot.
func TestMaybeWrapShellLCWithSnapshotRestoresRuntimeEnvLikeRust(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	env := map[string]string{
		"CODEX_THREAD_ID":             "thread-1",
		"CODEX_SESSION_ID":            "session-1",
		"CODEX_VERSION":               "1.2.3",
		"CODEX_PLUGIN_METRICS_OUTPUT": "/tmp/metrics.json",
		// Launch-context variables never get an override block.
		"CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN": "not-inherited",
		// Names a shell could not export again.
		"1INVALID": "ignored",
		"has-dash": "ignored",
	}
	rewritten := wrapSnapshotFixtureCommand(t, snapshotPath, map[string]string{"CUSTOM_OVERRIDE": "kept"}, env)
	script := rewritten[2]
	// The capture block covers the policy override and the runtime-only keys in
	// sorted order, with invalid and non-inheritable names left out.
	const wantCaptures = `__CODEX_SNAPSHOT_OVERRIDE_SET_0="${CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS+x}"
__CODEX_SNAPSHOT_OVERRIDE_0="${CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_1="${CODEX_PERMISSION_PROFILE+x}"
__CODEX_SNAPSHOT_OVERRIDE_1="${CODEX_PERMISSION_PROFILE-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_2="${CODEX_PLUGIN_METRICS_OUTPUT+x}"
__CODEX_SNAPSHOT_OVERRIDE_2="${CODEX_PLUGIN_METRICS_OUTPUT-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_3="${CODEX_SESSION_ID+x}"
__CODEX_SNAPSHOT_OVERRIDE_3="${CODEX_SESSION_ID-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_4="${CODEX_THREAD_ID+x}"
__CODEX_SNAPSHOT_OVERRIDE_4="${CODEX_THREAD_ID-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_5="${CODEX_VERSION+x}"
__CODEX_SNAPSHOT_OVERRIDE_5="${CODEX_VERSION-}"
__CODEX_SNAPSHOT_OVERRIDE_SET_6="${CUSTOM_OVERRIDE+x}"
__CODEX_SNAPSHOT_OVERRIDE_6="${CUSTOM_OVERRIDE-}"`
	if !strings.HasPrefix(script, wantCaptures+"\n\n") {
		t.Fatalf("override captures = \n%s\nwant \n%s", script, wantCaptures)
	}
	const wantRestore = `if [ -n "${__CODEX_SNAPSHOT_OVERRIDE_SET_6}" ]; then
  if [ -z "${CUSTOM_OVERRIDE+x}" ] || [ "${CUSTOM_OVERRIDE-}" != "${__CODEX_SNAPSHOT_OVERRIDE_6}" ]; then export CUSTOM_OVERRIDE="${__CODEX_SNAPSHOT_OVERRIDE_6}"; else export CUSTOM_OVERRIDE; fi
else builtin unset CUSTOM_OVERRIDE 2>/dev/null || command unset CUSTOM_OVERRIDE; fi
builtin unset __CODEX_SNAPSHOT_OVERRIDE_SET_6 __CODEX_SNAPSHOT_OVERRIDE_6 2>/dev/null || command unset __CODEX_SNAPSHOT_OVERRIDE_SET_6 __CODEX_SNAPSHOT_OVERRIDE_6`
	if !strings.Contains(script, wantRestore) {
		t.Fatalf("override restore is missing:\n%s", script)
	}
	if strings.Contains(script, `"${CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN+x}"`) {
		t.Fatalf("a non-inheritable variable was captured:\n%s", script)
	}
	if strings.Contains(script, "1INVALID+x") || strings.Contains(script, "has-dash+x") {
		t.Fatalf("an invalid variable name was captured:\n%s", script)
	}
}

// wrapSnapshotFixtureCommand runs the wrapper over the standard fixture
// command so the environment tests read at the level of the override rules.
func wrapSnapshotFixtureCommand(t *testing.T, snapshotPath string, overrides, env map[string]string) []string {
	t.Helper()
	return MaybeWrapShellLCWithSnapshot(
		[]string{"/bin/bash", "-lc", "echo hello"},
		&Shell{Type: ShellBash, Path: "/bin/bash"}, snapshotPath, overrides, env,
	)
}

// TestMaybeWrapShellLCWithSnapshotLeavesOtherCommandsAlone pins the cases Rust
// returns unchanged: no snapshot, a missing file, a short command, a non-login
// flag, a command already asking for a bare shell, and a brokered launch.
func TestMaybeWrapShellLCWithSnapshotLeavesOtherCommandsAlone(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	sessionShell := &Shell{Type: ShellBash, Path: "/bin/bash"}
	cases := []struct {
		name     string
		command  []string
		shell    *Shell
		snapshot string
		env      map[string]string
	}{
		{"no snapshot", []string{"/bin/bash", "-lc", "echo hi"}, sessionShell, "", nil},
		{"missing snapshot", []string{"/bin/bash", "-lc", "echo hi"}, sessionShell, filepath.Join(t.TempDir(), "absent.sh"), nil},
		{"short command", []string{"/bin/bash", "-lc"}, sessionShell, snapshotPath, nil},
		{"plain -c", []string{"/bin/bash", "-c", "echo hi"}, sessionShell, snapshotPath, nil},
		{"no session shell", []string{"/bin/bash", "-lc", "echo hi"}, nil, snapshotPath, nil},
		{"brokered launch", []string{"/bin/bash", "-lc", "echo hi"}, sessionShell, snapshotPath,
			map[string]string{network.CredentialBrokerActiveEnvKey: "1"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := MaybeWrapShellLCWithSnapshot(testCase.command, testCase.shell, testCase.snapshot, nil, testCase.env)
			if len(got) != len(testCase.command) {
				t.Fatalf("rewritten = %#v, want the original command", got)
			}
			for index := range got {
				if got[index] != testCase.command[index] {
					t.Fatalf("rewritten = %#v, want the original command", got)
				}
			}
		})
	}
}

// TestMaybeWrapShellLCWithSnapshotFollowsTheHostLikeRust pins the Windows guard:
// the wrapper's POSIX script is meaningless there, so the command is unchanged.
func TestMaybeWrapShellLCWithSnapshotFollowsTheHostLikeRust(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	snapshotWrapSupported = false
	command := []string{"/bin/bash", "-lc", "echo hi"}
	got := MaybeWrapShellLCWithSnapshot(command, &Shell{Type: ShellBash, Path: "/bin/bash"}, snapshotPath, nil, nil)
	if len(got) != 3 || got[2] != "echo hi" {
		t.Fatalf("rewritten = %#v, want the original command", got)
	}
}
