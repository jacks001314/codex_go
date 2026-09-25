package tool

// Shell-snapshot replay wrapping for `-lc` commands.
//
// Rust parity: codex-rs/core/src/tools/runtimes/mod.rs
// `maybe_wrap_shell_lc_with_snapshot`. Codex captures the user's shell state
// once per session and replays it in front of each command, so aliases,
// functions and options the user configured still apply inside model commands.
// The snapshot is sourced, not evaluated in the command's own environment, and
// the runtime's own variables are put back afterwards because the snapshot
// carries the user's values for them.

import (
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"codex_go/applypatch"
	"codex_go/envutil"
	"codex_go/execpolicy"
	"codex_go/network"
	"codex_go/plugin"
)

// Environment variables the launch's own values must win over: the snapshot
// holds whatever the user's shell had, while these describe the running turn.
// Names mirror Rust's exec_env and apply-patch constants.
const (
	codexSessionIDEnvVar      = "CODEX_SESSION_ID"
	codexVersionEnvVar        = "CODEX_VERSION"
	codexPermissionProfileVar = "CODEX_PERMISSION_PROFILE"
)

// snapshotReplayedEnvKeys are the runtime-only variables restored even when the
// live command environment does not carry them, so an inactive value cannot
// resurface from the snapshot.
var snapshotReplayedEnvKeys = []string{
	codexPermissionProfileVar,
	applypatch.PreserveLineEndingsEnvVar,
	plugin.PluginMetricsOutputEnvVar,
}

// snapshotWrapSupported mirrors Rust's `cfg!(windows)` guard, which leaves
// Windows commands alone because the wrapper's POSIX script is meaningless
// there. It is a variable so tests on any host can exercise the rewrite.
var snapshotWrapSupported = runtime.GOOS != "windows"

// MaybeWrapShellLCWithSnapshot rewrites a login-shell command so it sources the
// session's shell snapshot before running the model's script, or returns the
// command unchanged when no snapshot applies.
//
// explicitEnvOverrides are the policy-driven overrides that must win after the
// snapshot is sourced; env is the live command environment, which is the source
// for the runtime-only variables above.
//
// Rust additionally wraps brokered captures, where the snapshot is the only
// place the real credentials live and the wrapper replays protected
// environment files around it. Go has no brokered snapshot yet, so a brokered
// launch is left alone rather than sourcing a snapshot that could restore
// credentials the sandbox should not see.
func MaybeWrapShellLCWithSnapshot(
	command []string,
	sessionShell *Shell,
	snapshotPath string,
	explicitEnvOverrides map[string]string,
	env map[string]string,
) []string {
	if !snapshotWrapSupported {
		return command
	}
	if sessionShell == nil || strings.TrimSpace(sessionShell.Path) == "" {
		return command
	}
	snapshotPath = strings.TrimSpace(snapshotPath)
	if snapshotPath == "" {
		return command
	}
	if info, err := os.Stat(snapshotPath); err != nil || info.IsDir() {
		return command
	}
	if len(command) < 3 {
		return command
	}
	if env[network.CredentialBrokerActiveEnvKey] == "1" {
		return command
	}
	if command[1] != "-lc" {
		return command
	}

	overrideEnv := map[string]string{}
	for key, value := range explicitEnvOverrides {
		overrideEnv[key] = value
	}
	for _, key := range []string{
		codexSessionIDEnvVar,
		execpolicy.ThreadIDEnvVar,
		codexVersionEnvVar,
		codexPermissionProfileVar,
		applypatch.PreserveLineEndingsEnvVar,
		plugin.PluginMetricsOutputEnvVar,
	} {
		if value, ok := env[key]; ok {
			overrideEnv[key] = value
		}
	}
	overrideCaptures, overrideExports := buildSnapshotOverrideExports(overrideEnv, snapshotReplayedEnvKeys)

	quotedSnapshot := shellSingleQuote(snapshotPath)
	var trailing strings.Builder
	for _, argument := range command[3:] {
		trailing.WriteString(" '")
		trailing.WriteString(shellSingleQuote(argument))
		trailing.WriteString("'")
	}
	// The wrapper runs the caller's shell again so the caller's own script is
	// untouched by the snapshot replay, and inherits its arguments.
	runOriginal := "exec '" + shellSingleQuote(command[0]) + "' -c '" +
		shellSingleQuote(command[2]) + "'" + trailing.String()
	sourceSnapshot := "if . '" + quotedSnapshot + "' >/dev/null 2>&1; then :; fi"
	var rewritten strings.Builder
	if overrideExports != "" {
		rewritten.WriteString(overrideCaptures)
		rewritten.WriteString("\n\n")
	}
	rewritten.WriteString(sourceSnapshot)
	rewritten.WriteString("\n\n")
	if overrideExports != "" {
		rewritten.WriteString(overrideExports)
		rewritten.WriteString("\n\n")
	}
	rewritten.WriteString(runOriginal)

	return []string{sessionShell.Path, "-c", rewritten.String()}
}

// buildSnapshotOverrideExports captures the override values before the snapshot
// is sourced and restores them afterwards, mirroring Rust's
// build_override_exports: the capture is a copy so sourcing the snapshot cannot
// change what gets restored, and the restore unsets a variable the launch did
// not define.
func buildSnapshotOverrideExports(overrides map[string]string, restoreEvenWhenAbsent []string) (string, string) {
	keys := make([]string, 0, len(overrides)+len(restoreEvenWhenAbsent))
	seen := map[string]bool{}
	for key := range overrides {
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for _, key := range restoreEvenWhenAbsent {
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	filtered := keys[:0]
	for _, key := range keys {
		if !isValidShellVariableName(key) || envutil.IsNonInheritableEnvVar(key) {
			continue
		}
		filtered = append(filtered, key)
	}
	keys = filtered
	sort.Strings(keys)
	return buildSnapshotOverrideExportsForKeys("__CODEX_SNAPSHOT_OVERRIDE", keys)
}

func buildSnapshotOverrideExportsForKeys(variablePrefix string, keys []string) (string, string) {
	if len(keys) == 0 {
		return "", ""
	}
	captures := make([]string, 0, len(keys))
	restores := make([]string, 0, len(keys))
	for index, key := range keys {
		setVariable := variablePrefix + "_SET_" + strconv.Itoa(index)
		valueVariable := variablePrefix + "_" + strconv.Itoa(index)
		captures = append(captures, setVariable+"=\"${"+key+"+x}\"\n"+valueVariable+"=\"${"+key+"-}\"")
		restores = append(restores, "if [ -n \"${"+setVariable+"}\" ]; then\n"+
			"  if [ -z \"${"+key+"+x}\" ] || [ \"${"+key+"-}\" != \"${"+valueVariable+"}\" ]; then export "+key+"=\"${"+valueVariable+"}\"; else export "+key+"; fi\n"+
			"else builtin unset "+key+" 2>/dev/null || command unset "+key+"; fi\n"+
			"builtin unset "+setVariable+" "+valueVariable+" 2>/dev/null || command unset "+setVariable+" "+valueVariable)
	}
	return strings.Join(captures, "\n"), strings.Join(restores, "\n")
}

// shellSingleQuote escapes a value for a single-quoted shell word, mirroring
// Rust's shell_single_quote.
func shellSingleQuote(input string) string {
	return strings.ReplaceAll(input, "'", `'"'"'`)
}

func isValidShellVariableName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range []byte(name) {
		switch {
		case character == '_':
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case index > 0 && character >= '0' && character <= '9':
		default:
			return false
		}
	}
	return true
}
