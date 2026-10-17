// Package envutil guards model-reachable child processes against inheriting
// Codex launch context.
package envutil

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"codex_go/applypatch"
)

const (
	// CodexThreadIDEnvVar labels commands with the thread that launched them.
	// The value is informational for correlation, not an authorization token.
	CodexThreadIDEnvVar = "CODEX_THREAD_ID"
	// CodexToolCallIDEnvVar labels commands with the originating tool call.
	// The value is informational for correlation, not an authorization token.
	CodexToolCallIDEnvVar = "CODEX_TOOL_CALL_ID"
)

// CodexExecServerNoiseAuthTokenEnvVar is the execution-server credential that
// model-reachable commands and command hooks must not inherit. Mirrors Rust
// codex_protocol::shell_environment::CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN_ENV_VAR
// (the constant lives here so the exec server and the environment scrubber
// share one definition, mirroring the Rust move into codex-protocol #38941).
const CodexExecServerNoiseAuthTokenEnvVar = "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN"

// nonInheritableEnvVars mirrors Rust's NON_INHERITABLE_ENV_VARS
// (codex-rs/protocol/src/shell_environment.rs, Rust c4513cb982): environment
// variables that model-reachable child processes must not inherit.
var nonInheritableEnvVars = []string{
	CodexExecServerNoiseAuthTokenEnvVar,
	// Rust #39301: the Node REPL auth token must not reach model-reachable
	// child processes.
	"NODE_REPL_AUTH_TOKEN",
	// Rust #50019 `9552906b2b`: the guardian decisions API key must not be
	// forwarded into model-reachable child processes.
	"CODEX_GUARDIAN_DECISIONS_API_KEY",
	"OPENAI_FEDERATION_RULE_ID",
	"OPENAI_IDENTITY_TOKEN_FILE",
	"OPENAI_WORKLOAD_IDENTITY_CONTEXT",
}

// IsNonInheritableEnvVar reports whether name is a launch-context variable,
// matching case-insensitively like Rust's is_non_inheritable_env_var.
func IsNonInheritableEnvVar(name string) bool {
	for _, restricted := range nonInheritableEnvVars {
		if strings.EqualFold(restricted, strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// ScrubMap removes launch-context variables from a KEY->VALUE map
// (case-insensitive keys), preserving every other entry.
func ScrubMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return values
	}
	for key := range values {
		if IsNonInheritableEnvVar(key) {
			delete(values, key)
		}
	}
	return values
}

// ScrubSlice removes launch-context variables from an []string of "KEY=VALUE"
// pairs, matching keys case-insensitively.
func ScrubSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:0]
	for _, pair := range values {
		name := pair
		if idx := strings.IndexByte(pair, '='); idx >= 0 {
			name = pair[:idx]
		}
		if IsNonInheritableEnvVar(name) {
			continue
		}
		out = append(out, pair)
	}
	return out
}

// SetThreadIDEnv replaces the thread label in env. A nil threadID removes the
// label. Windows environment names are case-insensitive, so every case variant
// is removed before the canonical name is set.
func SetThreadIDEnv(env map[string]string, threadID *string) {
	if env == nil {
		return
	}
	removeEnvVar(env, CodexThreadIDEnvVar)
	if threadID != nil {
		env[CodexThreadIDEnvVar] = *threadID
	}
}

// SetToolCallIDEnv replaces the originating tool call label in env. A nil or
// NUL-containing toolCallID removes the label; an empty string is a valid ID
// and is preserved. Windows environment names are case-insensitive, so every
// case variant is removed before the canonical name is set.
func SetToolCallIDEnv(env map[string]string, toolCallID *string) {
	if env == nil {
		return
	}
	removeEnvVar(env, CodexToolCallIDEnvVar)
	if toolCallID == nil || strings.Contains(*toolCallID, "\x00") {
		return
	}
	env[CodexToolCallIDEnvVar] = *toolCallID
}

func removeEnvVar(env map[string]string, name string) {
	if env == nil {
		return
	}
	if runtime.GOOS == "windows" {
		for key := range env {
			if strings.EqualFold(key, name) {
				delete(env, key)
			}
		}
		return
	}
	delete(env, name)
}

// InjectApplyPatchEnv enables line-ending preservation for older standalone
// patch executables, including those provided by remote executors (Rust
// exec_env::inject_apply_patch_env, #51203). It removes any inherited or
// client-provided value (case-insensitively) and always sets the runtime
// variable so a stale value cannot restore the legacy normalization behavior.
func InjectApplyPatchEnv(env map[string]string) map[string]string {
	if env == nil {
		env = make(map[string]string)
	}
	for key := range env {
		if strings.EqualFold(key, applypatch.PreserveLineEndingsEnvVar) {
			delete(env, key)
		}
	}
	env[applypatch.PreserveLineEndingsEnvVar] = "1"
	return env
}

// ScrubCommandEnv prevents launch context from reaching a child command. When
// the command would inherit the process environment, it replaces it with the
// filtered environment; explicit overrides are filtered in place.
func ScrubCommandEnv(command *exec.Cmd) {
	if command == nil {
		return
	}
	if command.Env == nil {
		command.Env = ScrubSlice(os.Environ())
		return
	}
	command.Env = ScrubSlice(command.Env)
}
