package mcp

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"codex_go/envutil"
)

// This file ports the local stdio MCP server environment
// (rmcp-client::utils::create_env_for_mcp_server): the child process is
// spawned with a cleared environment containing only Rust's default variable
// set, the variables the config names through `env_vars`, the custom CA bundle
// settings, and the config's explicit `env` overrides. Previously Go spawned
// stdio servers with the whole parent environment, so every launch-context
// variable (and every unrelated secret) leaked into the server, and `env_vars`
// had no effect at all.

// mcpUnixDefaultEnvVars mirrors Rust rmcp-client::utils::DEFAULT_ENV_VARS for
// unix hosts.
var mcpUnixDefaultEnvVars = []string{
	"HOME",
	"LOGNAME",
	"PATH",
	"SHELL",
	"USER",
	"__CF_USER_TEXT_ENCODING",
	"LANG",
	"LC_ALL",
	"TERM",
	"TMPDIR",
	"TZ",
}

// mcpWindowsCoreEnvVars mirrors Rust
// codex_protocol::shell_environment::WINDOWS_CORE_ENV_VARS, which is what
// DEFAULT_ENV_VARS resolves to on Windows.
var mcpWindowsCoreEnvVars = []string{
	// Core path resolution
	"PATH",
	"PATHEXT",
	// Shell and system roots
	"SHELL",
	"COMSPEC",
	"SYSTEMROOT",
	"WINDIR",
	"SYSTEMDRIVE",
	// User context and profiles
	"USERNAME",
	"USERDOMAIN",
	"USERPROFILE",
	"HOMEDRIVE",
	"HOMEPATH",
	// Program locations
	"PROGRAMFILES",
	"PROGRAMFILES(X86)",
	"PROGRAMW6432",
	"PROGRAMDATA",
	// App data and caches
	"LOCALAPPDATA",
	"APPDATA",
	// Temp locations
	"TEMP",
	"TMP",
	"TMPDIR",
	// Common shells/pwsh hints
	"POWERSHELL",
	"PWSH",
}

// mcpStdioDefaultEnvVars returns Rust's DEFAULT_ENV_VARS for this platform.
func mcpStdioDefaultEnvVars() []string {
	if runtime.GOOS == "windows" {
		return mcpWindowsCoreEnvVars
	}
	return mcpUnixDefaultEnvVars
}

// mcpIsCustomCAEnvKey reports whether name is one of the curated CA bundle
// variables (Rust compares those case-insensitively on every platform).
func mcpIsCustomCAEnvKey(name string) bool {
	for _, key := range mcpCustomCAEnvKeys {
		if strings.EqualFold(key, strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// localStdioEnvVarNames ports Rust local_stdio_env_var_names: a local stdio
// server cannot reference the remote executor's environment, and
// launch-context variables are never inherited.
func localStdioEnvVarNames(envVars []EnvVar) ([]string, error) {
	names := make([]string, 0, len(envVars))
	for _, envVar := range envVars {
		name := strings.TrimSpace(envVar.Name)
		switch source := strings.TrimSpace(envVar.Source); source {
		case "":
		case "local":
		case "remote":
			return nil, fmt.Errorf("env_vars entry `%s` uses source `remote`, which requires remote MCP stdio", name)
		default:
			return nil, fmt.Errorf("unsupported env_vars source `%s`; expected `local` or `remote`", source)
		}
		if name == "" || envutil.IsNonInheritableEnvVar(name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// createEnvForMCPServer ports Rust rmcp-client::utils::create_env_for_mcp_server
// for the local placement: the caller spawns the server with exactly this
// environment (Rust's `env_clear().envs(&envs)`).
func createEnvForMCPServer(extraEnv map[string]string, envVars []EnvVar) (map[string]string, error) {
	additional, err := localStdioEnvVarNames(envVars)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(mcpStdioDefaultEnvVars())+len(additional)+len(extraEnv))
	for _, name := range mcpStdioDefaultEnvVars() {
		if value, ok := os.LookupEnv(name); ok {
			env[name] = value
		}
	}
	for _, name := range additional {
		if value, ok := os.LookupEnv(name); ok {
			env[name] = value
		}
	}
	for _, pair := range mcpCustomCAEnvPairs(extraEnv) {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if runtime.GOOS == "windows" {
			removeMCPEnvKeyFold(env, key)
		}
		env[key] = value
	}
	for name, value := range extraEnv {
		if runtime.GOOS == "windows" || mcpIsCustomCAEnvKey(name) {
			removeMCPEnvKeyFold(env, name)
		}
		env[name] = value
	}
	for name := range env {
		if envutil.IsNonInheritableEnvVar(name) {
			delete(env, name)
		}
	}
	return env, nil
}

// removeMCPEnvKeyFold removes every case-insensitive match of key.
func removeMCPEnvKeyFold(env map[string]string, key string) {
	for name := range env {
		if strings.EqualFold(name, key) {
			delete(env, name)
		}
	}
}
