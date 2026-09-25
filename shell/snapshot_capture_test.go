package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/execpolicy"
)

// TestSnapshotCaptureScriptsMatchRust pins the capture scripts: every marker is
// substituted, the declaration and environment records appear only when asked
// for, and the incremental variant groups options and aliases (#48078).
func TestSnapshotCaptureScriptsMatchRust(t *testing.T) {
	for _, shellType := range []ShellType{ShellBash, ShellZsh, ShellSh} {
		for _, declarations := range []bool{false, true} {
			options := SnapshotCaptureOptions{
				Startup:      SnapshotStartupInteractive,
				Declarations: declarations,
				Environment:  declarations,
			}
			script, ok := SnapshotCaptureScript(shellType, options)
			if !ok {
				t.Fatalf("SnapshotCaptureScript(%q) = not supported", shellType)
			}
			for _, marker := range []string{
				"SNAPSHOT_OPTIONS_BEGIN", "SNAPSHOT_ALIASES_END", "SNAPSHOT_EXPORTS",
				"SNAPSHOT_DECLARATION_ENVIRONMENT", "SNAPSHOT_STARTUP_ENVIRONMENT",
				"SNAPSHOT_COMMAND_HELPER", "SNAPSHOT_ENVIRONMENT", "SNAPSHOT_ZSH_ALIASES",
			} {
				if strings.Contains(script, marker) {
					t.Fatalf("%s capture script kept %s:\n%s", shellType, marker, script)
				}
			}
			if !strings.Contains(script, "# Snapshot file") || !strings.Contains(script, "# Functions") {
				t.Fatalf("%s capture script misses the banner:\n%s", shellType, script)
			}
			if !strings.Contains(script, "__codex_snapshot_command()") {
				t.Fatalf("%s capture script misses the command helper:\n%s", shellType, script)
			}
			if !strings.Contains(script, "# setopts ") {
				t.Fatalf("%s capture script misses the option count:\n%s", shellType, script)
			}
			declarationMarker := map[ShellType]string{
				ShellBash: "declare -xp",
				ShellZsh:  "typeset -xp",
				ShellSh:   "export -p | __codex_snapshot_command awk",
			}[shellType]
			if strings.Contains(script, declarationMarker) != declarations {
				t.Fatalf("%s declarations=%v but the export records disagree:\n%s", shellType, declarations, script)
			}
			if strings.Contains(script, "env\" -0") != declarations {
				t.Fatalf("%s declarations=%v but the environment record disagrees:\n%s", shellType, declarations, script)
			}
			// Interactive captures seed the user's startup configuration.
			switch shellType {
			case ShellBash:
				if !strings.Contains(script, "$HOME/.bashrc") {
					t.Fatalf("bash capture script misses the startup seeding:\n%s", script)
				}
			case ShellZsh:
				if !strings.Contains(script, "$ZDOTDIR/.zshrc") {
					t.Fatalf("zsh capture script misses the startup seeding:\n%s", script)
				}
			case ShellSh:
				if !strings.Contains(script, "__codex_snapshot_expand_env") {
					t.Fatalf("sh capture script misses the ENV expansion helper:\n%s", script)
				}
			}

			nonInteractive, ok := SnapshotCaptureScript(shellType, SnapshotCaptureOptions{
				Startup:      SnapshotStartupNonInteractive,
				Declarations: declarations,
			})
			if !ok {
				t.Fatalf("SnapshotCaptureScript(%q, non-interactive) = not supported", shellType)
			}
			if nonInteractive == script {
				t.Fatalf("%s non-interactive capture equals the interactive one", shellType)
			}

			source, ok := SnapshotSourceCaptureScript(shellType, options)
			if !ok {
				t.Fatalf("SnapshotSourceCaptureScript(%q) = not supported", shellType)
			}
			grouped := strings.Contains(source, "printf '{\\n'") &&
				strings.Contains(source, `printf "case '' in '') ;; esac\n}\n"`)
			if grouped != (shellType != ShellSh) {
				t.Fatalf("%s incremental capture grouped=%v:\n%s", shellType, grouped, source)
			}
			// Rust #48187: the sourced group serializes zsh aliases with
			// NO_RC_QUOTES, because the group is parsed before its restored
			// options take effect. Evaluating captures keep the plain alias dump.
			if shellType == ShellZsh {
				if !strings.Contains(script, "\\alias -L") {
					t.Fatalf("zsh capture script misses the alias dump:\n%s", script)
				}
				if strings.Contains(script, "NO_RC_QUOTES") {
					t.Fatalf("evaluating zsh capture must not change quoting options:\n%s", script)
				}
				if !strings.Contains(source, "(\\setopt NO_RC_QUOTES; \\alias -L)") {
					t.Fatalf("sourced zsh capture must normalize alias quoting:\n%s", source)
				}
			} else if strings.Contains(source, "NO_RC_QUOTES") {
				t.Fatalf("%s sourced capture must not set zsh options:\n%s", shellType, source)
			}
		}
	}
	if _, ok := SnapshotCaptureScript(ShellPowerShell, SnapshotCaptureOptions{}); ok {
		t.Fatal("PowerShell capture script must not be supported")
	}
	if _, ok := SnapshotCaptureScript(ShellCmd, SnapshotCaptureOptions{}); ok {
		t.Fatal("Cmd capture script must not be supported")
	}
}

// Mirrors Rust #48099's regression test: the render keeps only the exports the
// shell environment policy admits, preserves multiline values, and never persists
// the captured original of a variable the policy sets explicitly.
func TestRenderScriptAppliesShellEnvironmentPolicyLikeRust(t *testing.T) {
	captured := ParseCapturedSnapshot(ShellBash, []byte(strings.Join([]string{
		"# Snapshot file\n# Functions\n",
		"",
		"PROFILE_ALLOWED", "declare -x PROFILE_ALLOWED=$'first\\nsecond'",
		"PROFILE_TOKEN", `declare -x PROFILE_TOKEN="token-sentinel"`,
		"PROFILE_DENIED", `declare -x PROFILE_DENIED="denied-sentinel"`,
		"OTHER", `declare -x OTHER="other-sentinel"`,
		"PROFILE_SECRET", `declare -x PROFILE_SECRET="original-secret-sentinel"`,
		"", "",
	}, "\x00")))
	if captured == nil {
		t.Fatal("ParseCapturedSnapshot() = nil")
	}
	ignoreDefaultExcludes := false
	policy := &execpolicy.EnvPolicy{
		Inherit:               "all",
		IgnoreDefaultExcludes: &ignoreDefaultExcludes,
		Exclude: []execpolicy.EnvVariablePattern{
			{Mode: execpolicy.EnvPatternLiteral, Value: "PROFILE_DENIED"},
		},
		IncludeOnly: []execpolicy.EnvVariablePattern{
			{Mode: execpolicy.EnvPatternLiteral, Value: "PATH"},
			{Mode: execpolicy.EnvPatternWildcard, Value: "PROFILE_*"},
		},
		Set: map[string]string{"PROFILE_SECRET": "dummy"},
	}
	script := captured.RenderScript(policy)
	if !strings.Contains(script, "declare -x PROFILE_ALLOWED=$'first\\nsecond'") {
		t.Fatalf("a multiline allowed export was dropped: %q", script)
	}
	for _, sentinel := range []string{"token-sentinel", "denied-sentinel", "other-sentinel", "original-secret-sentinel"} {
		if strings.Contains(script, sentinel) {
			t.Fatalf("the snapshot retains %s: %q", sentinel, script)
		}
	}
	if strings.Contains(script, "PROFILE_TOKEN") || strings.Contains(script, "PROFILE_DENIED") ||
		strings.Contains(script, "OTHER") || strings.Contains(script, "PROFILE_SECRET") {
		t.Fatalf("the snapshot declares a filtered or overridden export: %q", script)
	}

	// The default policy keeps every captured declaration.
	unrestricted := captured.RenderScript(nil)
	for _, sentinel := range []string{"token-sentinel", "denied-sentinel", "other-sentinel", "original-secret-sentinel"} {
		if !strings.Contains(unrestricted, sentinel) {
			t.Fatalf("the default policy dropped %s: %q", sentinel, unrestricted)
		}
	}
}

// TestParseCapturedSnapshotDecodesRecords covers the record grammar, including
// the incomplete-stream rejections Rust fails closed on.
func TestParseCapturedSnapshotDecodesRecords(t *testing.T) {
	stream := strings.Join([]string{
		"banner\n# Snapshot file\nalias h='echo hi'\n", // state (banner stripped)
		"export FOO='bar'\n",                           // aliases
		"FOO", "declare -x FOO=\"bar\"",                // export record
		"COUNT", "declare -x COUNT=\"1\"",
		"",           // end of exports
		"A=1\x00B=2", // environment
	}, "\x00")
	captured := ParseCapturedSnapshot(ShellBash, []byte(stream))
	if captured == nil {
		t.Fatalf("ParseCapturedSnapshot() = nil for %q", stream)
	}
	if captured.ShellType != ShellBash {
		t.Fatalf("ShellType = %q", captured.ShellType)
	}
	if captured.State != "# Snapshot file\nalias h='echo hi'\n" {
		t.Fatalf("State = %q", captured.State)
	}
	if captured.Aliases != "export FOO='bar'\n" {
		t.Fatalf("Aliases = %q", captured.Aliases)
	}
	if len(captured.Exports) != 2 || captured.Exports[0].Key != "FOO" ||
		captured.Exports[0].Source != `declare -x FOO="bar"` || captured.Exports[1].Key != "COUNT" {
		t.Fatalf("Exports = %#v", captured.Exports)
	}
	if string(captured.Environment) != "A=1\x00B=2" {
		t.Fatalf("Environment = %q", captured.Environment)
	}
	script := captured.RenderScript(nil)
	if !strings.HasPrefix(script, "# Snapshot file\nalias h='echo hi'\nexport FOO='bar'\n# exports (native declarations)\n") {
		t.Fatalf("RenderScript() = %q", script)
	}
	if captured.RenderState() != captured.State+captured.Aliases {
		t.Fatalf("RenderState() = %q", captured.RenderState())
	}

	// POSIX sh captures declarations as environment records and records the
	// startup file it sourced.
	shStream := snapshotStartupMarker + "/home/user/.profile\x00" + "PATH=/usr/bin\x00\x00" +
		"# Snapshot file\nalias a=b\n\x00" + "alias a=b\n\x00" + "\x00" +
		"FOO=bar\x00PWD=/tmp\x00BAD-NAME=x\x00\x00"
	shCaptured := ParseCapturedSnapshot(ShellSh, []byte(shStream))
	if shCaptured == nil {
		t.Fatalf("ParseCapturedSnapshot(sh) = nil for %q", shStream)
	}
	if shCaptured.StartupEnvironment == nil || shCaptured.StartupEnvironment.Path != "/home/user/.profile" {
		t.Fatalf("StartupEnvironment = %#v", shCaptured.StartupEnvironment)
	}
	// Rust's slice keeps each environment entry's NUL and drops only the record
	// list's final terminator.
	if string(shCaptured.StartupEnvironment.Environment) != "PATH=/usr/bin\x00" {
		t.Fatalf("StartupEnvironment.Environment = %q", shCaptured.StartupEnvironment.Environment)
	}
	if len(shCaptured.Exports) != 1 || shCaptured.Exports[0].Source != "export FOO=bar\n" {
		t.Fatalf("sh exports = %#v", shCaptured.Exports)
	}

	// Incomplete streams and unsupported shells are rejected.
	if ParseCapturedSnapshot(ShellBash, []byte("no record")) != nil {
		t.Fatal("a stream without a record boundary must not parse")
	}
	if ParseCapturedSnapshot(ShellBash, []byte("no banner\x00aliases\x00\x00")) != nil {
		t.Fatal("a stream without the snapshot banner must not parse")
	}
	if ParseCapturedSnapshot(ShellPowerShell, []byte(stream)) != nil {
		t.Fatal("PowerShell captures must not parse")
	}
}

// TestSnapshotCaptureReplaysWithRealBash runs the real capture script through
// bash, parses the stream, renders the snapshot, and sources it: a function and
// an exported variable defined before the capture must be available afterwards,
// which is what the snapshot exists for.
func TestSnapshotCaptureReplaysWithRealBash(t *testing.T) {
	bash := snapshotTestBashPath(t)
	if bash == "" {
		t.Skip("no bash on this host")
	}
	prelude := filepath.Join(t.TempDir(), "prelude.bash")
	preludeBody := "snapshot_probe_function() { echo probe-ok; }\n" +
		"alias snapshot_probe_alias='echo alias-ok'\n" +
		"export SNAPSHOT_PROBE_MARKER=probe-value\n"
	if err := os.WriteFile(prelude, []byte(preludeBody), 0o600); err != nil {
		t.Fatalf("write prelude error = %v", err)
	}
	script, ok := SnapshotCaptureScript(ShellBash, SnapshotCaptureOptions{
		Startup:      SnapshotStartupNonInteractive,
		Declarations: true,
		Environment:  true,
	})
	if !ok {
		t.Fatal("bash capture script is not supported")
	}
	command := exec.Command(bash, "-c", script)
	command.Env = append(os.Environ(), "BASH_ENV="+prelude)
	captured, err := command.Output()
	if err != nil {
		t.Fatalf("capture run error = %v", err)
	}
	snapshot := ParseCapturedSnapshot(ShellBash, captured)
	if snapshot == nil {
		t.Fatalf("ParseCapturedSnapshot() = nil for a real capture:\n%s", captured)
	}
	if !strings.Contains(snapshot.State, "snapshot_probe_function") {
		t.Fatalf("captured state misses the function:\n%s", snapshot.State)
	}
	if !strings.Contains(snapshot.Aliases, "snapshot_probe_alias") {
		t.Fatalf("captured aliases miss the alias:\n%s", snapshot.Aliases)
	}
	exported := false
	for _, export := range snapshot.Exports {
		if export.Key == "SNAPSHOT_PROBE_MARKER" && strings.Contains(export.Source, "probe-value") {
			exported = true
		}
	}
	if !exported {
		t.Fatalf("capture misses the exported variable: %#v", snapshot.Exports)
	}
	if !strings.Contains(string(snapshot.Environment), "SNAPSHOT_PROBE_MARKER=probe-value") {
		t.Fatalf("capture misses the environment record:\n%q", snapshot.Environment)
	}

	snapshotPath := filepath.Join(t.TempDir(), "shell_snapshots", "session.1.sh")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o700); err != nil {
		t.Fatalf("create snapshot dir error = %v", err)
	}
	if err := os.WriteFile(snapshotPath, []byte(snapshot.RenderScript(nil)), 0o600); err != nil {
		t.Fatalf("write snapshot error = %v", err)
	}
	replay := exec.Command(bash, "-c", "set -e; . \""+snapshotPath+"\"; snapshot_probe_function; printf ' marker=%s\\n' \"$SNAPSHOT_PROBE_MARKER\"")
	output, err := replay.Output()
	if err != nil {
		t.Fatalf("replay error = %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "probe-ok") || !strings.Contains(string(output), "marker=probe-value") {
		t.Fatalf("replay output = %q", output)
	}
}

// snapshotTestBashPath finds a bash binary for the capture round trip.
func snapshotTestBashPath(t *testing.T) string {
	t.Helper()
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
