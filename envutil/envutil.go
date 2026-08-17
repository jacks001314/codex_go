// Package envutil guards model-reachable child processes against inheriting
// Codex launch context.
package envutil

import (
	"os"
	"os/exec"
	"strings"

	"codex_go/applypatch"
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

// InjectApplyPatchEnv carries the configured apply-patch line-ending rollout
// state into child-process environments (Rust exec_env::inject_apply_patch_env,
// c9c6c0daa9). It removes any inherited or client-provided value
// (case-insensitively) and only sets the runtime variable when preservation is
// enabled, keeping the active feature configuration authoritative.
func InjectApplyPatchEnv(env map[string]string, preserveLineEndings bool) map[string]string {
	if env == nil {
		env = make(map[string]string)
	}
	for key := range env {
		if strings.EqualFold(key, applypatch.PreserveLineEndingsEnvVar) {
			delete(env, key)
		}
	}
	if preserveLineEndings {
		env[applypatch.PreserveLineEndingsEnvVar] = "1"
	}
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
