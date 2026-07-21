package mcp

import "path/filepath"

// SandboxState mirrors Rust's codex_mcp::runtime::SandboxState.
// It describes the current sandbox policy that MCP servers should be aware of.
type SandboxState struct {
	PermissionProfile    string `json:"permissionProfile"`
	CodexLinuxSandboxExe string `json:"codexLinuxSandboxExe,omitempty"`
	SandboxCWD           string `json:"sandboxCwd"`
	UseLegacyLandlock    *bool  `json:"useLegacyLandlock,omitempty"`
}

// BuildSandboxStateEnv returns environment variable overrides that forward
// sandbox state to a child MCP server process. This mirrors how Rust passes
// the initial permission profile to MCP server processes.
func BuildSandboxStateEnv(state *SandboxState) map[string]string {
	if state == nil {
		return nil
	}
	env := map[string]string{
		"CODEX_PERMISSION_PROFILE": state.PermissionProfile,
	}
	if state.SandboxCWD != "" {
		env["CODEX_SANDBOX_CWD"] = filepath.ToSlash(state.SandboxCWD)
	}
	if state.CodexLinuxSandboxExe != "" {
		env["CODEX_LINUX_SANDBOX_EXE"] = state.CodexLinuxSandboxExe
	}
	if state.UseLegacyLandlock != nil && *state.UseLegacyLandlock {
		env["CODEX_USE_LEGACY_LANDLOCK"] = "1"
	}
	return env
}

// MergeSandboxEnv merges sandbox state environment variables into an existing
// env map, returning a new combined map.
func MergeSandboxEnv(base map[string]string, state *SandboxState) map[string]string {
	merged := make(map[string]string, len(base)+4)
	for key, value := range base {
		merged[key] = value
	}
	sandbox := BuildSandboxStateEnv(state)
	for key, value := range sandbox {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	return merged
}
