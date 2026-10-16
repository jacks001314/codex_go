package mcp

import (
	"encoding/json"
)

// MCP sandbox metadata contract (Rust codex_mcp::sandbox_metadata, #53709).
//
// Servers advertise one of the two state capabilities to receive the opaque
// sandbox policy in tool-call `_meta`. During the migration both names carry
// byte-identical values; neither contains executable discovery data or
// deprecation messages. The launcher envelope lives in its own reserved key so
// policy stays separate from the launcher used to apply it.
const (
	// mcpSandboxStateCapability is the capability servers advertise to receive
	// sandbox state (Rust MCP_SANDBOX_STATE_CAPABILITY).
	mcpSandboxStateCapability = "codex/sandbox-state"
	// mcpSandboxStateMetaCapability is the deprecated capability and metadata
	// spelling, retained as an identical alias (Rust
	// MCP_SANDBOX_STATE_META_CAPABILITY).
	mcpSandboxStateMetaCapability = "codex/sandbox-state-meta"
	// mcpSandboxLauncherMetaKey is tool-call metadata describing a sandbox
	// launcher on the MCP server's host (Rust MCP_SANDBOX_LAUNCHER_META_KEY).
	mcpSandboxLauncherMetaKey = "codex/sandbox-launcher"
)

// mcpSandboxLauncherExecutableField is the only launcher field consumers may
// interpret (Rust SandboxLauncher::codex_executable).
const mcpSandboxLauncherExecutableField = "codexExecutable"

// augmentSandboxMetadata mirrors Rust sandbox_metadata::augment_metadata.
//
// The identical state object is written under both state keys, every unrelated
// entry is preserved, reserved launcher metadata left by an earlier call is
// removed, and the launcher envelope is inserted only when the host verified an
// executable for this server's environment. A `_meta` value that is neither
// absent nor an object is left untouched, exactly as Rust returns early when
// the value is not a JSON object.
func augmentSandboxMetadata(meta any, state *SandboxState, executable string) any {
	if state == nil {
		return meta
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return meta
	}
	var stateValue any
	if err := json.Unmarshal(encoded, &stateValue); err != nil {
		return meta
	}
	out := map[string]any{}
	switch current := meta.(type) {
	case nil:
	case map[string]any:
		if cloned := cloneAnyMap(current); cloned != nil {
			out = cloned
		}
	default:
		return meta
	}
	out[mcpSandboxStateCapability] = stateValue
	if stateMap, ok := stateValue.(map[string]any); ok {
		out[mcpSandboxStateMetaCapability] = cloneAnyMap(stateMap)
	} else {
		out[mcpSandboxStateMetaCapability] = stateValue
	}
	// Reserved launcher metadata must not survive from a previous or local call
	// when this server's execution environment has no verified executable.
	delete(out, mcpSandboxLauncherMetaKey)
	if executable != "" {
		out[mcpSandboxLauncherMetaKey] = map[string]any{mcpSandboxLauncherExecutableField: executable}
	}
	return out
}
