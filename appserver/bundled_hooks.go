package appserver

import "strings"

// Bundled plugin cleanup-hook allowlist (Rust plugin/src/bundled_hooks.rs).
//
// Temporary cleanup-hook allowlist shared by local and executor plugin
// discovery. It narrowly authorizes known MCP cleanup calls; it does not verify
// plugin signatures. Keep unsigned plugin exceptions together so they can be
// removed as signing lands.
type bundledHook struct {
	pluginID string
	events   []HookEventName
	// server/tool identify the MCP tool. connectorID is empty for a plain
	// MCP-server target and set for an Apps target, which additionally requires
	// the caller's registered connector.
	server      string
	tool        string
	connectorID string
}

var allowlistedBundledHooks = []bundledHook{
	{
		pluginID: "browser@openai-bundled",
		events:   []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:   "node_repl",
		tool:     "turn_ended",
	},
	{
		pluginID: "chrome@openai-bundled",
		events:   []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:   "node_repl",
		tool:     "turn_ended",
	},
	{
		pluginID: "chrome-dev@openai-bundled",
		events:   []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:   "node_repl",
		tool:     "turn_ended",
	},
	{
		pluginID: "chrome-internal@openai-bundled",
		events:   []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:   "node_repl",
		tool:     "turn_ended",
	},
	{
		pluginID: "computer-use@openai-bundled",
		events:   []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:   "node_repl",
		tool:     "turn_ended",
	},
	{
		pluginID: "unified-computer-use@openai-bundled",
		events:   []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:   "cua_repl",
		tool:     "turn_ended",
	},
	{
		pluginID:    "browser@openai-curated-remote",
		events:      []HookEventName{HookEventStop, HookEventInterrupt, HookEventSubagentStop},
		server:      "codex_apps",
		tool:        "browser.turn_ended",
		connectorID: "connector_openai_browser",
	},
}

// isAllowlistedBundledCleanupHook mirrors Rust
// `is_allowlisted_bundled_cleanup_hook`. App targets additionally require the
// connector identity for this handler's server and tool from the caller's
// enabled tool catalog; callers without that catalog must pass nil.
func isAllowlistedBundledCleanupHook(pluginID string, event HookEventName, matcher *string, handler hookJSONHandlerConfigWire, appConnectorID *string) bool {
	if handler.hookHandlerType() != HookHandlerMCPTool {
		return false
	}
	if matcher != nil {
		return false
	}
	server := strings.TrimSpace(handler.Server)
	toolName := strings.TrimSpace(handler.Tool)
	for _, hook := range allowlistedBundledHooks {
		if hook.pluginID != pluginID || !bundledHookCoversEvent(hook, event) {
			continue
		}
		if server != hook.server || toolName != hook.tool {
			continue
		}
		if hook.connectorID == "" {
			return true
		}
		// Raw Apps tool names can collide; require the registered connector and
		// reject manifest-provided arguments.
		if len(handler.Input) != 0 {
			continue
		}
		if appConnectorID != nil && strings.TrimSpace(*appConnectorID) == hook.connectorID {
			return true
		}
	}
	return false
}

func bundledHookCoversEvent(hook bundledHook, event HookEventName) bool {
	for _, candidate := range hook.events {
		if candidate == event {
			return true
		}
	}
	return false
}

// hideBuiltinHooks drops builtin entries from a public hooks-list response
// (Rust app-server catalog_processor.rs hooks_to_info). The discovered hooks
// themselves are untouched, so the engine still runs builtin cleanup hooks.
func hideBuiltinHooks(response *HookListResponse) *HookListResponse {
	if response == nil {
		return nil
	}
	cloned := &HookListResponse{Data: make([]HookListEntry, len(response.Data))}
	for i := range response.Data {
		entry := response.Data[i]
		hooks := entry.Hooks
		filtered := make([]HookMetadata, 0, len(hooks))
		for _, hook := range hooks {
			if hook.Builtin {
				continue
			}
			filtered = append(filtered, hook)
		}
		entry.Hooks = filtered
		cloned.Data[i] = entry
	}
	return cloned
}
