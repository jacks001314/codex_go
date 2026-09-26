package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/network"
	"codex_go/sandbox"
	"codex_go/shell"
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
	rewritten := MaybeWrapShellLCWithSnapshot(command, sessionShell, snapshotPath, nil, nil, nil)
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
		sessionShell, snapshotPath, nil, nil, nil,
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
		&Shell{Type: ShellZsh, Path: "/bin/zsh"}, snapshotPath, nil, nil, nil,
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
		&Shell{Type: ShellBash, Path: "/bin/bash"}, snapshotPath, overrides, env, nil,
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
			got := MaybeWrapShellLCWithSnapshot(testCase.command, testCase.shell, testCase.snapshot, nil, testCase.env, nil)
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
	got := MaybeWrapShellLCWithSnapshot(command, &Shell{Type: ShellBash, Path: "/bin/bash"}, snapshotPath, nil, nil, nil)
	if len(got) != 3 || got[2] != "echo hi" {
		t.Fatalf("rewritten = %#v, want the original command", got)
	}
}

// TestShellExecutorReplaysTheSessionSnapshotLikeRust wires the provider through
// a launch: the model's `-lc` command sources the session snapshot, the
// provider sees the launch facts Rust passes, and a provider that returns
// nothing leaves the command alone.
func TestShellExecutorReplaysTheSessionSnapshotLikeRust(t *testing.T) {
	snapshotPath := snapshotWrapFixture(t)
	workspaceWrite := sandbox.WorkspaceWritePermissionProfile()
	var launched *ShellRequest
	var requested *SnapshotProviderRequest
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: pluginMetricsShellRunner{onRun: func(req *ShellRequest) { launched = req }},
		Shell:  &Shell{Type: ShellBash, Path: "/bin/bash"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:      sandbox.ApprovalOnRequest,
			AllowLoginShell:     true,
			CWD:                 t.TempDir(),
			DefaultTimeoutMS:    5000,
			PermissionProfile:   &workspaceWrite,
			PermissionProfileID: "resolved",
		},
		SnapshotProvider: func(_ context.Context, request SnapshotProviderRequest) string {
			captured := request
			requested = &captured
			return snapshotPath
		},
	})
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-snapshot",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if launched == nil {
		t.Fatal("the shell runner did not see the launch request")
	}
	if requested == nil {
		t.Fatal("the snapshot provider was not asked for a snapshot")
	}
	if requested.ShellType != ShellBash || requested.ShellPath != "/bin/bash" ||
		requested.CWD != launched.CWD || !requested.AllowLoginShell || requested.Remote ||
		requested.PermissionProfileID != "resolved" {
		t.Fatalf("provider request = %#v", requested)
	}
	if len(launched.Command) != 3 || launched.Command[0] != "/bin/bash" || launched.Command[1] != "-c" {
		t.Fatalf("launch command = %#v, want the snapshot wrapper", launched.Command)
	}
	if !strings.Contains(launched.Command[2], "if . '"+snapshotPath+"' >/dev/null 2>&1; then :; fi") ||
		!strings.Contains(launched.Command[2], "exec '/bin/bash' -c 'echo hi'") {
		t.Fatalf("launch command does not replay the snapshot:\n%s", launched.Command[2])
	}

	// A provider without a snapshot (feature disabled, a shell that overrides the
	// session's, or a remote environment) leaves the launch untouched.
	var untouched *ShellRequest
	plain := NewShellExecutor(&ShellExecutorOptions{
		Runner: pluginMetricsShellRunner{onRun: func(req *ShellRequest) { untouched = req }},
		Shell:  &Shell{Type: ShellBash, Path: "/bin/bash"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:    sandbox.ApprovalOnRequest,
			AllowLoginShell:   true,
			CWD:               t.TempDir(),
			DefaultTimeoutMS:  5000,
			PermissionProfile: &workspaceWrite,
		},
		SnapshotProvider: func(context.Context, SnapshotProviderRequest) string { return "" },
	})
	if _, err := plain.Execute(context.Background(), &Invocation{
		CallID:   "call-no-snapshot",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if untouched == nil || len(untouched.Command) != 3 || untouched.Command[2] != "echo hi" {
		t.Fatalf("launch command = %#v, want the original command", untouched)
	}
}

// Mirrors Rust's `build_brokered_credential_exports` (#48073): the snapshot's
// brokered credential copies are restored onto their real keys with xtrace
// suppressed, and the unset markers re-remove keys the broker stripped.
func TestBuildBrokeredCredentialExportsLikeRust(t *testing.T) {
	if got := buildBrokeredCredentialExports(nil, false); got != "" {
		t.Fatalf("empty env exports = %q, want empty", got)
	}
	valueKey := snapshotBrokeredValueEnvPrefix + "OPENAI_API_KEY"
	unsetKey := snapshotBrokeredUnsetEnvPrefix + "STRIPPED_TOKEN"
	env := map[string]string{valueKey: "dummy", unsetKey: "1", snapshotBrokeredValueEnvPrefix + "1BAD": "ignored"}

	outer := buildBrokeredCredentialExports(env, false)
	for _, want := range []string{
		"case $- in\n  *x*) __CODEX_SNAPSHOT_BROKER_XTRACE=1; set +x ;;",
		"if [ -z \"${" + valueKey + "+x}\" ]; then exit 1; fi",
		"export OPENAI_API_KEY=\"${" + valueKey + "}\" || exit 1",
		"unset STRIPPED_TOKEN || exit 1",
		"unset __CODEX_SNAPSHOT_BROKER_XTRACE",
	} {
		if !strings.Contains(outer, want) {
			t.Fatalf("outer exports missing %q:\n%s", want, outer)
		}
	}
	if strings.Contains(outer, "1BAD") {
		t.Fatalf("invalid variable name was not filtered:\n%s", outer)
	}
	if strings.Contains(outer, "unset "+valueKey) {
		t.Fatalf("outer block removed the snapshot copy:\n%s", outer)
	}

	inner := buildBrokeredCredentialExports(env, true)
	for _, want := range []string{
		"unset " + valueKey + " || exit 1",
		"unset " + unsetKey + " || exit 1",
	} {
		if !strings.Contains(inner, want) {
			t.Fatalf("inner exports missing %q:\n%s", want, inner)
		}
	}
}

// Mirrors Rust's `join_shell_blocks`: empty fragments are dropped and the rest
// joined with a single newline.
func TestJoinShellBlocksLikeRust(t *testing.T) {
	if got := joinShellBlocks("", "a", "", "b"); got != "a\nb" {
		t.Fatalf("joinShellBlocks = %q, want %q", got, "a\nb")
	}
	if got := joinShellBlocks("", ""); got != "" {
		t.Fatalf("joinShellBlocks(empty) = %q, want empty", got)
	}
}

// Mirrors Rust's `build_proxy_env_exports`: the managed proxy variables are
// captured around the snapshot and restored afterwards, and BASH_ENV joins them
// only for a brokered launch.
func TestBuildProxyEnvExportsLikeRust(t *testing.T) {
	captures, restores := buildProxyEnvExports(map[string]string{})
	for _, want := range []string{
		"__CODEX_SNAPSHOT_PROXY_OVERRIDE_SET_0=",
		"CODEX_NETWORK_PROXY_ACTIVE",
		"HTTP_PROXY",
		"http_proxy",
		"ALL_PROXY",
		"__CODEX_SNAPSHOT_PROXY_ENV_SET=\"${" + network.ProxyActiveEnvKey + "+x}\"",
	} {
		if !strings.Contains(captures, want) {
			t.Fatalf("captures missing %q:\n%s", want, captures)
		}
	}
	if !strings.HasPrefix(restores, "if [ -n \"$__CODEX_SNAPSHOT_PROXY_ENV_SET\" ] || [ -n \"${"+network.ProxyActiveEnvKey+"+x}\" ]; then") {
		t.Fatalf("restores guard = %q", restores)
	}
	if strings.Contains(captures, "BASH_ENV") {
		t.Fatalf("non-brokered captures included BASH_ENV:\n%s", captures)
	}
	if !strings.Contains(captures, network.ProxyCustomCAEnvKeys[0]) {
		t.Fatalf("captures missing the custom CA key %q:\n%s", network.ProxyCustomCAEnvKeys[0], captures)
	}

	brokered, _ := buildProxyEnvExports(map[string]string{network.CredentialBrokerActiveEnvKey: "1"})
	if !strings.Contains(brokered, "BASH_ENV") {
		t.Fatalf("brokered captures missing BASH_ENV:\n%s", brokered)
	}
}

func TestProxyEnvKeysLikeRust(t *testing.T) {
	found := map[string]bool{}
	for _, key := range network.ProxyEnvKeys {
		found[key] = true
	}
	for _, want := range []string{
		network.ProxyActiveEnvKey,
		network.CredentialBrokerActiveEnvKey,
		network.BrokeredCredentialsEnvKey,
		network.ProxyAllowLocalBindingEnvKey,
		network.ProxyAttributionTokenEnvKey,
		"HTTP_PROXY", "ALL_PROXY", "NO_PROXY",
	} {
		if !found[want] {
			t.Fatalf("ProxyEnvKeys missing %q", want)
		}
	}
}

// Mirrors Rust's `posix_env_path_expansion_function` export for the brokered
// wrapper.
func TestPosixEnvPathExpansionFunctionExportedLikeRust(t *testing.T) {
	if script := shell.PosixEnvPathExpansionFunction(); !strings.Contains(script, "__codex_snapshot_expand_env") {
		t.Fatalf("expansion function = %q", script)
	}
}

// Mirrors the brokered `env_captures`/`env_exports` of Rust's
// `maybe_wrap_shell_lc_with_snapshot`: the protected POSIX ENV is captured with
// ZDOTDIR-aware expansion and restored only while the command's ENV still points
// at one of those startup files.
func TestBuildBrokeredEnvScriptLikeRust(t *testing.T) {
	captures, replayed, exports := buildBrokeredEnvScript(ShellBash, nil)
	for _, want := range []string{
		"__CODEX_SNAPSHOT_ORIGINAL_ENV_SET=\"${ENV+x}\"",
		"__codex_snapshot_expand_env_with_zdotdir() (",
		"if [ -n \"${" + snapshotOriginalZdotdirEnvKey + "+x}\" ]; then",
		"__CODEX_SNAPSHOT_PROTECTED_ENV=$(",
		"__codex_snapshot_expand_env_with_zdotdir \"${" + snapshotOriginalBashEnvKey + "-}\"",
		"__codex_snapshot_expand_env_with_zdotdir \"${" + snapshotOriginalPosixEnvKey + "-}\"",
	} {
		if !strings.Contains(captures, want) {
			t.Fatalf("captures missing %q:\n%s", want, captures)
		}
	}
	if !strings.Contains(captures, "__codex_snapshot_expand_env() (") {
		t.Fatalf("captures missing the expansion helper:\n%s", captures)
	}
	if replayed != "__CODEX_SNAPSHOT_REPLAYED_BASH_ENV=\"${BASH_ENV-}\"" {
		t.Fatalf("replayed capture = %q", replayed)
	}
	for _, want := range []string{
		"__codex_snapshot_env_is_protected() (",
		"if __codex_snapshot_env_is_protected \"$__CODEX_SNAPSHOT_CURRENT_ENV\"; then",
		"builtin unset ENV 2>/dev/null || command unset ENV || exit 1",
		"  " + snapshotOriginalBashEnvKey + " " + snapshotOriginalPosixEnvKey,
	} {
		if !strings.Contains(exports, want) {
			t.Fatalf("exports missing %q:\n%s", want, exports)
		}
	}
}
