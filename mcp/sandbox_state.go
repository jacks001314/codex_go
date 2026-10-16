package mcp

import (
	"encoding/json"
	"path/filepath"
)

// SandboxState mirrors Rust's codex_mcp::SandboxState
// (codex-rs/codex-mcp/src/sandbox_metadata.rs, #53709/#52182).
//
// It is the opaque policy that MCP servers forward unchanged to
// `codex sandbox --sandbox-state-json`; consumers must preserve unknown fields
// instead of deserializing and rebuilding the object, so the permission profile
// stays raw JSON here. Launcher information never belongs in this object: the
// host advertises a verified CLI separately under mcpSandboxLauncherMetaKey
// (#53709).
type SandboxState struct {
	// PermissionProfile is the opaque policy payload forwarded to the sandbox
	// CLI. It is written under the camelCase key, which is the authoritative
	// spelling (Rust serializes SandboxState with rename_all = "camelCase").
	PermissionProfile    json.RawMessage `json:"permissionProfile,omitempty"`
	CodexLinuxSandboxExe string          `json:"codexLinuxSandboxExe,omitempty"`
	SandboxCWD           string          `json:"sandboxCwd"`
	UseLegacyLandlock    *bool           `json:"useLegacyLandlock,omitempty"`
	// UseMXC carries the effective Windows sandbox backend captured for the
	// server's environment (Rust #49781's `use_mxc`, default false). The sandbox
	// CLI selects the native MXC backend from it, so a parent's CLI-override
	// selection survives the handoff.
	UseMXC bool `json:"useMxc"`
}

// BuildSandboxStateEnv returns environment variable overrides that forward
// sandbox state to a child MCP server process.
//
// Residual: this helper is a Go-only carrier with no production caller; Rust
// forwards the state in tool-call `_meta` (see augmentSandboxMetadata) instead
// of environment variables.
func BuildSandboxStateEnv(state *SandboxState) map[string]string {
	if state == nil {
		return nil
	}
	env := map[string]string{
		"CODEX_PERMISSION_PROFILE": string(state.PermissionProfile),
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
