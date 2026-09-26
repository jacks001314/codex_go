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
	"codex_go/shell"
)

// Environment variables the launch's own values must win over: the snapshot
// holds whatever the user's shell had, while these describe the running turn.
// Names mirror Rust's exec_env and apply-patch constants.
const (
	codexSessionIDEnvVar      = "CODEX_SESSION_ID"
	codexVersionEnvVar        = "CODEX_VERSION"
	codexPermissionProfileVar = "CODEX_PERMISSION_PROFILE"
)

// Brokered replay prefixes mirror Rust's SNAPSHOT_BROKERED_*_ENV_PREFIX: a
// brokered snapshot stores child-visible dummy values (and unset markers) under
// these names, and the wrapper restores them onto the real keys around the
// command (#48073).
const (
	snapshotBrokeredValueEnvPrefix = "CODEX_NETWORK_PROXY_SNAPSHOT_BROKERED_VALUE_"
	snapshotBrokeredUnsetEnvPrefix = "CODEX_NETWORK_PROXY_SNAPSHOT_BROKERED_UNSET_"
)

// snapshotProxyOverrideVariablePrefix mirrors the capture prefix Rust uses for
// the managed proxy variables.
const snapshotProxyOverrideVariablePrefix = "__CODEX_SNAPSHOT_PROXY_OVERRIDE"

// The macOS-only git SSH command marker Rust preserves across the snapshot.
const (
	proxyGitSSHCommandEnvKey = "GIT_SSH_COMMAND"
	proxyGitSSHCommandMarker = "CODEX_PROXY_GIT_SSH_COMMAND=1 "
)

// Startup-environment keys a brokered snapshot preserves around the command,
// mirroring Rust's SNAPSHOT_ORIGINAL_*_ENV_KEY constants.
const (
	snapshotOriginalBashEnvKey    = "CODEX_NETWORK_PROXY_SNAPSHOT_ORIGINAL_BASH_ENV"
	snapshotOriginalPosixEnvKey   = "CODEX_NETWORK_PROXY_SNAPSHOT_ORIGINAL_POSIX_ENV"
	snapshotOriginalZdotdirEnvKey = "CODEX_NETWORK_PROXY_SNAPSHOT_ORIGINAL_ZDOTDIR"
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
	runtimePathPrepends []string,
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
	brokered := env[network.CredentialBrokerActiveEnvKey] == "1"
	flag := command[1]
	if flag != "-lc" && !(brokered && flag == "-c") {
		return command
	}
	if brokered {
		// A brokered launch is rewrapped only once its snapshot was captured with
		// the broker's protected environment (the SNAPSHOT_* capture keys). Until
		// the creation side lands, sourcing a plain snapshot could reintroduce
		// credentials the sandbox must not see.
		if !brokeredSnapshotPrepared(env) {
			return command
		}
		return brokeredSnapshotWrap(command, sessionShell, snapshotPath, explicitEnvOverrides, env, runtimePathPrepends)
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
	// A snapshot carries the user's PATH, so the runtime's own entries are
	// re-exported after sourcing it (Rust's shell_exports_after_snapshot).
	pathExports := (*RuntimePathPrepends)(nil)
	if len(runtimePathPrepends) > 0 {
		pathExports = &RuntimePathPrepends{entries: append([]string(nil), runtimePathPrepends...)}
	}
	runtimePathExports := pathExports.ShellExportsAfterSnapshot(explicitEnvOverrides)

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
	if runtimePathExports != "" {
		rewritten.WriteString(runtimePathExports)
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

// joinShellBlocks joins non-empty shell fragments with a single newline,
// mirroring Rust's `join_shell_blocks`.
func joinShellBlocks(blocks ...string) string {
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block != "" {
			kept = append(kept, block)
		}
	}
	return strings.Join(kept, "\n")
}

// buildBrokeredCredentialExports mirrors Rust's
// `build_brokered_credential_exports`: the snapshot's brokered credential copies
// are restored onto their real keys while the command runs, and its unset markers
// re-remove keys the broker had stripped. xtrace is suppressed around the block
// so a replayed value can never reach the terminal. `removeCopies` drops the
// snapshot-side copies for the inner (command) block.
func buildBrokeredCredentialExports(env map[string]string, removeCopies bool) string {
	type copyKey struct {
		key     string
		copyKey string
	}
	valueCopies := make([]copyKey, 0)
	for copyKeyName := range env {
		key, ok := strings.CutPrefix(copyKeyName, snapshotBrokeredValueEnvPrefix)
		if !ok || !isValidShellVariableName(key) {
			continue
		}
		valueCopies = append(valueCopies, copyKey{key: key, copyKey: copyKeyName})
	}
	sort.Slice(valueCopies, func(i int, j int) bool {
		if valueCopies[i].key != valueCopies[j].key {
			return valueCopies[i].key < valueCopies[j].key
		}
		return valueCopies[i].copyKey < valueCopies[j].copyKey
	})
	valueRestores := make([]string, 0, len(valueCopies))
	for _, entry := range valueCopies {
		restore := "if [ -z \"${" + entry.copyKey + "+x}\" ]; then exit 1; fi\n" +
			"if [ -n \"${" + entry.key + "+x}\" ] && [ -n \"${" + entry.key + "}\" ] && [ \"${" + entry.key + "}\" != \"${" + entry.copyKey + "}\" ]; then export " + entry.key + "=\"${" + entry.copyKey + "}\" || exit 1; fi\n" +
			"if [ -n \"${" + entry.key + "+x}\" ] && [ -n \"${" + entry.key + "}\" ] && [ \"${" + entry.key + "}\" != \"${" + entry.copyKey + "}\" ]; then exit 1; fi"
		if removeCopies {
			restore += "\nunset " + entry.copyKey + " || exit 1"
		}
		valueRestores = append(valueRestores, restore)
	}

	unsetMarkers := make([]copyKey, 0)
	for markerKey := range env {
		key, ok := strings.CutPrefix(markerKey, snapshotBrokeredUnsetEnvPrefix)
		if !ok || !isValidShellVariableName(key) {
			continue
		}
		unsetMarkers = append(unsetMarkers, copyKey{key: key, copyKey: markerKey})
	}
	sort.Slice(unsetMarkers, func(i int, j int) bool {
		if unsetMarkers[i].key != unsetMarkers[j].key {
			return unsetMarkers[i].key < unsetMarkers[j].key
		}
		return unsetMarkers[i].copyKey < unsetMarkers[j].copyKey
	})
	unsetRestores := make([]string, 0, len(unsetMarkers))
	for _, entry := range unsetMarkers {
		restore := "if [ -z \"${" + entry.copyKey + "+x}\" ]; then exit 1; fi\n" +
			"if [ -n \"${" + entry.key + "+x}\" ] && [ -n \"${" + entry.key + "}\" ]; then unset " + entry.key + " || exit 1; fi\n" +
			"if [ -n \"${" + entry.key + "+x}\" ] && [ -n \"${" + entry.key + "}\" ]; then exit 1; fi"
		if removeCopies {
			restore += "\nunset " + entry.copyKey + " || exit 1"
		}
		unsetRestores = append(unsetRestores, restore)
	}

	exports := joinShellBlocks(strings.Join(valueRestores, "\n"), strings.Join(unsetRestores, "\n"))
	if exports == "" {
		return ""
	}
	return "case $- in\n  *x*) __CODEX_SNAPSHOT_BROKER_XTRACE=1; set +x ;;\n  *) __CODEX_SNAPSHOT_BROKER_XTRACE= ;;\nesac\n" +
		exports +
		"\nif [ -n \"$__CODEX_SNAPSHOT_BROKER_XTRACE\" ]; then\n  unset __CODEX_SNAPSHOT_BROKER_XTRACE\n  set -x\nelse\n  unset __CODEX_SNAPSHOT_BROKER_XTRACE\nfi"
}

// buildProxyEnvExports mirrors Rust's `build_proxy_env_exports`: it captures the
// managed proxy variables (plus the brokered credential keys, the custom CA keys
// and `BASH_ENV` for a brokered launch) so sourcing the snapshot cannot replace
// the live values, and restores them once the snapshot is in effect, together
// with the macOS git-ssh marker handling.
func buildProxyEnvExports(env map[string]string) (string, string) {
	keys := append([]string(nil), network.ProxyEnvKeys...)
	keys = append(keys, network.ProxyBrokeredCredentialEnvKeys(env)...)
	keys = append(keys, network.ProxyCustomCAEnvKeys...)
	if env[network.CredentialBrokerActiveEnvKey] == "1" {
		keys = append(keys, "BASH_ENV")
	}
	keys = normalizedShellVariableKeys(keys)
	captures, restores := buildSnapshotOverrideExportsForKeys(snapshotProxyOverrideVariablePrefix, keys)
	activeKey := network.ProxyActiveEnvKey
	proxyCaptures := captures + "\n__CODEX_SNAPSHOT_PROXY_ENV_SET=\"${" + activeKey + "+x}\""
	proxyRestores := "if [ -n \"$__CODEX_SNAPSHOT_PROXY_ENV_SET\" ] || [ -n \"${" + activeKey + "+x}\" ]; then\n" +
		restores + "\nfi"
	gitCaptures, gitRestores := buildCodexProxyGitSSHCommandExports()
	return joinShellBlocks(proxyCaptures, gitCaptures), joinShellBlocks(proxyRestores, gitRestores)
}

// normalizedShellVariableKeys filters invalid names, then sorts and dedupes,
// mirroring Rust's key preparation before `build_override_exports_for_keys`.
func normalizedShellVariableKeys(keys []string) []string {
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if isValidShellVariableName(key) {
			filtered = append(filtered, key)
		}
	}
	sort.Strings(filtered)
	out := filtered[:0]
	for index, key := range filtered {
		if index > 0 && filtered[index-1] == key {
			continue
		}
		out = append(out, key)
	}
	return out
}

// buildCodexProxyGitSSHCommandExports mirrors Rust's
// `build_codex_proxy_git_ssh_command_exports`, which exists only on macOS: the
// proxy's marked GIT_SSH_COMMAND must survive the snapshot while an ordinary user
// value must not be mistaken for it.
func buildCodexProxyGitSSHCommandExports() (string, string) {
	if runtime.GOOS != "darwin" {
		return "", ""
	}
	key := proxyGitSSHCommandEnvKey
	markerPattern := strings.TrimRight(proxyGitSSHCommandMarker, " ") + "\\ *"
	captures := "__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_SET=\"${" + key + "+x}\"\n" +
		"__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND=\"${" + key + "-}\"\n" +
		"case \"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND\" in\n  " + markerPattern +
		") __CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_LIVE_MARKED=1 ;;\n  *) __CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_LIVE_MARKED= ;;\nesac"
	restores := "case \"${" + key + "-}\" in\n  " + markerPattern +
		") __CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_AFTER_MARKED=1 ;;\n  *) __CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_AFTER_MARKED= ;;\nesac\n" +
		"if [ -n \"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_LIVE_MARKED\" ]; then\n" +
		"  if [ -z \"${" + key + "+x}\" ] || [ -n \"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_AFTER_MARKED\" ]; then\n" +
		"    export " + key + "=\"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND\"\n  fi\n" +
		"elif [ -n \"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_AFTER_MARKED\" ]; then\n" +
		"  if [ -n \"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND_SET\" ]; then\n" +
		"    export " + key + "=\"$__CODEX_SNAPSHOT_PROXY_GIT_SSH_COMMAND\"\n" +
		"  else\n    unset " + key + "\n  fi\nfi"
	return captures, restores
}

// buildBrokeredEnvScript mirrors the brokered branch of Rust's
// `maybe_wrap_shell_lc_with_snapshot`: before the snapshot runs it captures the
// launch's protected POSIX `ENV` (following ZDOTDIR-aware `${VAR}` indirections
// through the expansion helper), and afterwards it restores `ENV` only while the
// command's own `ENV` still points at one of those protected startup files.
//
// Go's shell model collapses `sh` into bash, so the POSIX startup key is only
// selected once a dedicated `sh` type exists (Rust's `ShellType::Sh` branch).
func buildBrokeredEnvScript(shellType ShellType, env map[string]string) (string, string, string) {
	bashEnvKey := snapshotOriginalBashEnvKey
	posixEnvKey := snapshotOriginalPosixEnvKey
	startupEnvKey := bashEnvKey
	if shellType == ShellType("sh") && hasEnvKey(env, posixEnvKey) {
		startupEnvKey = posixEnvKey
	}
	alternateStartupEnvKey := posixEnvKey
	if startupEnvKey == posixEnvKey {
		alternateStartupEnvKey = bashEnvKey
	}
	captures := strings.NewReplacer(
		"%%EXPAND%%", shell.PosixEnvPathExpansionFunction(),
		"%%ZDOTDIR%%", snapshotOriginalZdotdirEnvKey,
		"%%STARTUP%%", startupEnvKey,
		"%%ALTERNATE%%", alternateStartupEnvKey,
	).Replace(brokeredEnvCapturesTemplate)
	replayed := "__CODEX_SNAPSHOT_REPLAYED_BASH_ENV=\"${BASH_ENV-}\""
	exports := strings.NewReplacer(
		"%%BASH%%", bashEnvKey,
		"%%POSIX%%", posixEnvKey,
	).Replace(brokeredEnvExportsTemplate)
	return captures, replayed, exports
}

func hasEnvKey(env map[string]string, key string) bool {
	_, ok := env[key]
	return ok
}

// brokeredSnapshotPrepared reports whether the live launch carries the
// broker-prepared capture keys the brokered wrapper restores from. It stands in
// for the creation side (which writes the same keys into a brokered snapshot's
// launch environment) so a plain snapshot is never replayed under the broker.
func brokeredSnapshotPrepared(env map[string]string) bool {
	for key := range env {
		if strings.HasPrefix(key, snapshotBrokeredValueEnvPrefix) ||
			strings.HasPrefix(key, snapshotBrokeredUnsetEnvPrefix) ||
			key == snapshotOriginalBashEnvKey ||
			key == snapshotOriginalPosixEnvKey ||
			key == snapshotOriginalZdotdirEnvKey {
			return true
		}
	}
	return false
}

// brokeredSnapshotWrap mirrors the brokered branch of Rust's
// `maybe_wrap_shell_lc_with_snapshot`: the session shell sources the snapshot and
// then runs the command inline (reusing the shell) once the broker's protected
// environment has been replayed around it, or re-executes the original shell
// with the brokered zsh startup flags.
func brokeredSnapshotWrap(
	command []string,
	sessionShell *Shell,
	snapshotPath string,
	explicitEnvOverrides map[string]string,
	env map[string]string,
	runtimePathPrepends []string,
) []string {
	flag := command[1]
	shellPath := sessionShell.Path
	commandUsesSessionZsh := sessionShell.Type == ShellZsh && command[0] == shellPath
	reuseSessionShell := command[0] == shellPath && (sessionShell.Type == ShellBash || sessionShell.Type == ShellZsh)
	originalShellIsZsh := commandUsesSessionZsh || DetectShellType(command[0]) == ShellZsh
	brokeredZshFlag := "-fc"
	if flag == "-lc" {
		brokeredZshFlag = "-lfc"
	}
	originalShellFlag := "-c"
	if originalShellIsZsh {
		originalShellFlag = brokeredZshFlag
	}
	var trailing strings.Builder
	for _, argument := range command[3:] {
		trailing.WriteString(" '")
		trailing.WriteString(shellSingleQuote(argument))
		trailing.WriteString("'")
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
	proxyCaptures, proxyExports := buildProxyEnvExports(env)
	envCaptures, replayedStartupCapture, envExports := buildBrokeredEnvScript(sessionShell.Type, env)
	zshStartupExports := ""
	if sessionShell.Type == ShellZsh {
		key := snapshotOriginalZdotdirEnvKey
		zshStartupExports = "if [ -n \"${" + key + "+x}\" ]; then\n  export ZDOTDIR=\"${" + key + "}\"\nelif [ \"${ZDOTDIR-}\" = /dev/null ]; then\n  unset ZDOTDIR\nfi\nunset " + key
	}
	// Zsh always reads the global zshenv, even with `-f`, so private copies of the
	// child-visible dummy values survive until the command shell finished startup.
	outerCredentialExports := buildBrokeredCredentialExports(env, false)
	innerCredentialExports := buildBrokeredCredentialExports(env, true)
	originalScript := command[2]
	if innerCredentialExports != "" {
		originalScript = innerCredentialExports + "\n" + command[2]
	}
	pathExports := (*RuntimePathPrepends)(nil)
	if len(runtimePathPrepends) > 0 {
		pathExports = &RuntimePathPrepends{entries: append([]string(nil), runtimePathPrepends...)}
	}
	runtimePathPrependExports := pathExports.ShellExportsAfterSnapshot(explicitEnvOverrides)

	overrideCaptures = joinShellBlocks(outerCredentialExports, overrideCaptures, proxyCaptures, envCaptures)
	overrideExports = joinShellBlocks(
		outerCredentialExports,
		replayedStartupCapture,
		overrideExports,
		proxyExports,
		runtimePathPrependExports,
		envExports,
		zshStartupExports,
		outerCredentialExports,
	)
	runOriginal := originalScript
	if !reuseSessionShell {
		runOriginal = "exec '" + shellSingleQuote(command[0]) + "' " + originalShellFlag + " '" +
			shellSingleQuote(originalScript) + "'" + trailing.String()
	}
	quotedSnapshot := shellSingleQuote(snapshotPath)
	rewrittenScript := "if . '" + quotedSnapshot + "' >/dev/null 2>&1; then :; fi\n\n" + runOriginal
	if overrideExports != "" {
		rewrittenScript = overrideCaptures + "\n\nif . '" + quotedSnapshot + "' >/dev/null 2>&1; then :; fi\n\n" +
			overrideExports + "\n\n" + runOriginal
	}
	wrapperFlag := "-c"
	if sessionShell.Type == ShellZsh {
		wrapperFlag = brokeredZshFlag
	}
	rewritten := []string{shellPath, wrapperFlag, rewrittenScript}
	rewritten = append(rewritten, command[3:]...)
	return rewritten
}

const brokeredEnvCapturesTemplate = `__CODEX_SNAPSHOT_ORIGINAL_ENV_SET="${ENV+x}"
__CODEX_SNAPSHOT_ORIGINAL_ENV="${ENV-}"
%%EXPAND%%
__codex_snapshot_expand_env_with_zdotdir() (
  if [ -n "${%%ZDOTDIR%%+x}" ]; then
    export ZDOTDIR="${%%ZDOTDIR%%}"
  elif [ -n "${ZSH_VERSION-}" ] && [ "${ZDOTDIR-}" = /dev/null ]; then
    unset ZDOTDIR
  fi
  __codex_snapshot_expand_env "$1"
)
__CODEX_SNAPSHOT_PROTECTED_ENV=$(
  __codex_snapshot_expand_env_with_zdotdir "${%%STARTUP%%-}"
)
__CODEX_SNAPSHOT_ALTERNATE_PROTECTED_ENV=$(
  __codex_snapshot_expand_env_with_zdotdir "${%%ALTERNATE%%-}"
)`

const brokeredEnvExportsTemplate = `__CODEX_SNAPSHOT_CURRENT_ENV=$(
  __codex_snapshot_expand_env_with_zdotdir "${ENV-}"
)
__CODEX_SNAPSHOT_ORIGINAL_EXPANDED_ENV=$(
  __codex_snapshot_expand_env_with_zdotdir "$__CODEX_SNAPSHOT_ORIGINAL_ENV"
)
__CODEX_SNAPSHOT_REPLAYED_PROTECTED_ENV=$(
  __codex_snapshot_expand_env_with_zdotdir "$__CODEX_SNAPSHOT_REPLAYED_BASH_ENV"
)
unset -f __codex_snapshot_expand_env __codex_snapshot_expand_env_with_zdotdir
__codex_snapshot_env_is_protected() (
  for __codex_protected_env in \
    "$__CODEX_SNAPSHOT_PROTECTED_ENV" \
    "$__CODEX_SNAPSHOT_ALTERNATE_PROTECTED_ENV" \
    "$__CODEX_SNAPSHOT_REPLAYED_PROTECTED_ENV"; do
    if [ -n "$__codex_protected_env" ] &&
      { [ "$1" = "$__codex_protected_env" ] ||
        [ "$1" -ef "$__codex_protected_env" ] 2>/dev/null; }; then
      return 0
    fi
  done
  return 1
)
if __codex_snapshot_env_is_protected "$__CODEX_SNAPSHOT_CURRENT_ENV"; then
  if [ -n "$__CODEX_SNAPSHOT_ORIGINAL_ENV_SET" ] &&
    ! __codex_snapshot_env_is_protected "$__CODEX_SNAPSHOT_ORIGINAL_EXPANDED_ENV"; then
    builtin export ENV="$__CODEX_SNAPSHOT_ORIGINAL_ENV" 2>/dev/null ||
      command export ENV="$__CODEX_SNAPSHOT_ORIGINAL_ENV" || exit 1
    [ "${ENV-}" = "$__CODEX_SNAPSHOT_ORIGINAL_ENV" ] || exit 1
  else
    builtin unset ENV 2>/dev/null || command unset ENV || exit 1
    [ -z "${ENV+x}" ] || exit 1
  fi
fi
unset -f __codex_snapshot_env_is_protected
unset __CODEX_SNAPSHOT_ORIGINAL_ENV_SET __CODEX_SNAPSHOT_ORIGINAL_ENV \
  __CODEX_SNAPSHOT_PROTECTED_ENV __CODEX_SNAPSHOT_ALTERNATE_PROTECTED_ENV \
  __CODEX_SNAPSHOT_REPLAYED_BASH_ENV __CODEX_SNAPSHOT_REPLAYED_PROTECTED_ENV \
  __CODEX_SNAPSHOT_CURRENT_ENV __CODEX_SNAPSHOT_ORIGINAL_EXPANDED_ENV \
  %%BASH%% %%POSIX%%`
