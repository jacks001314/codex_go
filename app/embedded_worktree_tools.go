package app

import (
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/session"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/app_server_session.rs (#50148).
//
// The embedded TUI runs its app server in process, so its turns never pass
// through an AppServerSession request handle. It still hosts the managed
// worktree MCP service: the service talks to the in-process router and its
// config is merged into every turn's overrides.

// embeddedWorktreeMCPHost is the worktree MCP service of the running embedded
// TUI. One process hosts one interactive session, and the turn builder reads
// this host when it assembles the per-turn configuration overrides.
var embeddedWorktreeMCPHost atomic.Pointer[taskToolsMCPHost]

// startEmbeddedWorktreeMCPHost builds and starts the worktree-only MCP server,
// or returns nil when the session must not advertise the tools.
func startEmbeddedWorktreeMCPHost(root *cli.RootOptions, state *codextui.State, store *session.Store) *taskToolsMCPHost {
	tools := interactiveEmbeddedWorktreeTools(root, state, store)
	if tools == nil {
		return nil
	}
	server, err := startManagedWorktreeMCPServer(tools)
	if err != nil {
		return nil
	}
	return &taskToolsMCPHost{server: server, worktrees: tools}
}

// installEmbeddedWorktreeMCPHost publishes the host for the session's turns and
// returns the function that restores the previous registry entry and closes the
// hosted server.
func installEmbeddedWorktreeMCPHost(host *taskToolsMCPHost) func() {
	previous := embeddedWorktreeMCPHost.Swap(host)
	return func() {
		embeddedWorktreeMCPHost.Store(previous)
		host.close()
	}
}

// embeddedWorktreeMCPConfigOverrides renders the hosted server as the "-c"
// overrides the embedded runner applies on every turn.
func embeddedWorktreeMCPConfigOverrides() []string {
	host := embeddedWorktreeMCPHost.Load()
	if host == nil {
		return nil
	}
	values := host.configValues()
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	overrides := []string{}
	for _, key := range keys {
		flattenMCPConfigOverrides(key, values[key], &overrides)
	}
	return overrides
}

func flattenMCPConfigOverrides(prefix string, value any, out *[]string) {
	if nested, ok := value.(map[string]any); ok {
		keys := make([]string, 0, len(nested))
		for key := range nested {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenMCPConfigOverrides(prefix+"."+key, nested[key], out)
		}
		return
	}
	*out = append(*out, prefix+"="+formatMCPConfigOverrideValue(value))
}

func formatMCPConfigOverrideValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		return ""
	}
}

// interactiveEmbeddedWorktreeTools builds the managed-worktree service for a
// trusted local embedded session, or returns nil when the session must not
// advertise the tools (Rust AppServerSession::start_dynamic_tool_mcp worktree
// eligibility).
func interactiveEmbeddedWorktreeTools(root *cli.RootOptions, state *codextui.State, store *session.Store) *managedWorktreeTools {
	if store == nil {
		return nil
	}
	settings, ok := interactiveWorktreeSettings(root)
	if !ok {
		return nil
	}
	cwd := ""
	if state != nil {
		cwd = strings.TrimSpace(state.CWD)
	}
	if cwd == "" && root != nil {
		cwd = strings.TrimSpace(root.Shared.CWD)
	}
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return nil
	}
	if cwd != "" {
		if level, ok := config.ProjectTrustLevelForTarget(loaded.Values, cwd); ok && strings.EqualFold(level, "untrusted") {
			return nil
		}
	}
	if remoteTUIToolsNamespaceConflict(loaded.Values, DynamicToolNamespace) {
		return nil
	}
	return newEmbeddedManagedWorktreeTools(store, settings)
}
