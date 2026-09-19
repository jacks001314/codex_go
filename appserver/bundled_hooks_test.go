package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/plugin"
)

func bundledHookHandler(server string, tool string, input map[string]any) hookJSONHandlerConfigWire {
	return hookJSONHandlerConfigWire{Type: string(HookHandlerMCPTool), Server: server, Tool: tool, Input: input}
}

func stringPointerForHooks(value string) *string { return &value }

// Mirrors Rust plugin/src/bundled_hooks.rs: only the known cleanup handlers of
// unsigned bundled plugins are allowlisted, matched exactly by plugin id, event,
// server, and tool.
func TestAllowlistedBundledCleanupHooksLikeRust(t *testing.T) {
	matcher := "Bash"
	for _, tc := range []struct {
		name          string
		pluginID      string
		event         HookEventName
		matcher       *string
		handler       hookJSONHandlerConfigWire
		appConnector  *string
		wantAllowlist bool
	}{
		{
			name:          "bundled node repl cleanup",
			pluginID:      "browser@openai-bundled",
			event:         HookEventStop,
			handler:       bundledHookHandler("node_repl", "turn_ended", nil),
			wantAllowlist: true,
		},
		{
			name:          "interrupt is allowlisted",
			pluginID:      "browser@openai-bundled",
			event:         HookEventInterrupt,
			handler:       bundledHookHandler("node_repl", "turn_ended", nil),
			wantAllowlist: true,
		},
		{
			name:          "subagent stop is allowlisted",
			pluginID:      "unified-computer-use@openai-bundled",
			event:         HookEventSubagentStop,
			handler:       bundledHookHandler("cua_repl", "turn_ended", nil),
			wantAllowlist: true,
		},
		{
			name:     "event outside the allowlist",
			pluginID: "browser@openai-bundled",
			event:    HookEventPreToolUse,
			handler:  bundledHookHandler("node_repl", "turn_ended", nil),
		},
		{
			name:     "colliding tool name",
			pluginID: "browser@openai-bundled",
			event:    HookEventStop,
			handler:  bundledHookHandler("node_repl", "other_tool", nil),
		},
		{
			name:     "colliding server name",
			pluginID: "browser@openai-bundled",
			event:    HookEventStop,
			handler:  bundledHookHandler("other_repl", "turn_ended", nil),
		},
		{
			name:     "unknown plugin",
			pluginID: "third-party@example",
			event:    HookEventStop,
			handler:  bundledHookHandler("node_repl", "turn_ended", nil),
		},
		{
			name:     "matcher present",
			pluginID: "browser@openai-bundled",
			event:    HookEventStop,
			matcher:  &matcher,
			handler:  bundledHookHandler("node_repl", "turn_ended", nil),
		},
		{
			name:     "command handler",
			pluginID: "browser@openai-bundled",
			event:    HookEventStop,
			handler:  hookJSONHandlerConfigWire{Type: string(HookHandlerCommand), Command: "echo hi"},
		},
		{
			name:     "apps target without a connector catalog",
			pluginID: "browser@openai-curated-remote",
			event:    HookEventStop,
			handler:  bundledHookHandler("codex_apps", "browser.turn_ended", nil),
		},
		{
			name:          "apps target with the registered connector",
			pluginID:      "browser@openai-curated-remote",
			event:         HookEventStop,
			handler:       bundledHookHandler("codex_apps", "browser.turn_ended", map[string]any{}),
			appConnector:  stringPointerForHooks("connector_openai_browser"),
			wantAllowlist: true,
		},
		{
			name:         "apps target with a colliding connector",
			pluginID:     "browser@openai-curated-remote",
			event:        HookEventStop,
			handler:      bundledHookHandler("codex_apps", "browser.turn_ended", nil),
			appConnector: stringPointerForHooks("connector_other_browser"),
		},
		{
			name:     "apps target with manifest-provided arguments",
			pluginID: "browser@openai-curated-remote",
			event:    HookEventStop,
			handler:  bundledHookHandler("codex_apps", "browser.turn_ended", map[string]any{"untrusted": "input"}),
			// The connector matches, but a non-empty input still rejects it.
			appConnector: stringPointerForHooks("connector_openai_browser"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := isAllowlistedBundledCleanupHook(tc.pluginID, tc.event, tc.matcher, tc.handler, tc.appConnector)
			if got != tc.wantAllowlist {
				t.Fatalf("isAllowlistedBundledCleanupHook(%s, %s) = %v, want %v", tc.pluginID, tc.event, got, tc.wantAllowlist)
			}
		})
	}
}

// Mirrors Rust's local discovery path: an allowlisted bundled cleanup hook is
// builtin (trusted and enabled without a recorded hash) but omitted from the
// public hooks list, while an ordinary plugin hook keeps its trust state.
func TestBundledCleanupHookIsBuiltinAndHiddenFromThePublicListLikeRust(t *testing.T) {
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hooksDir, "hooks.json")
	body := `{"hooks":{"Stop":[{"hooks":[{"type":"mcp_tool","server":"node_repl","tool":"turn_ended","input":{}}]},` +
		`{"hooks":[{"type":"mcp_tool","server":"node_repl","tool":"not_allowlisted","input":{}}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewHookDiscoveryService("")
	service.McpToolHooksEnabled = true
	service.PluginHookSources = []plugin.HookSource{{
		PluginID:           "browser@openai-bundled",
		PluginRoot:         root,
		SourcePath:         path,
		SourceRelativePath: "hooks/hooks.json",
	}}
	discovered := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
	if len(discovered.Data) != 1 || len(discovered.Data[0].Hooks) != 2 {
		t.Fatalf("discovered hooks = %#v", discovered.Data)
	}
	var builtin, ordinary HookMetadata
	for _, hook := range discovered.Data[0].Hooks {
		switch {
		case hook.Tool != nil && *hook.Tool == "turn_ended":
			builtin = hook
		case hook.Tool != nil && *hook.Tool == "not_allowlisted":
			ordinary = hook
		}
	}
	if !builtin.Builtin || !builtin.Enabled || builtin.TrustStatus != HookTrustTrusted {
		t.Fatalf("allowlisted hook = %#v, want builtin/trusted/enabled", builtin)
	}
	if ordinary.Builtin || ordinary.TrustStatus != HookTrustUntrusted {
		t.Fatalf("ordinary hook = %#v, want a non-builtin untrusted hook", ordinary)
	}

	// The engine keeps the discovered hooks; only the public list hides the
	// builtin entry.
	public := hideBuiltinHooks(discovered)
	if len(public.Data) != 1 || len(public.Data[0].Hooks) != 1 {
		t.Fatalf("public hooks = %#v", public.Data)
	}
	if public.Data[0].Hooks[0].Tool == nil || *public.Data[0].Hooks[0].Tool != "not_allowlisted" {
		t.Fatalf("public hook = %#v, want the non-allowlisted handler", public.Data[0].Hooks[0])
	}
	// The discovered response itself is untouched for the engine.
	if !discovered.Data[0].Hooks[0].Builtin {
		t.Fatal("hiding builtin hooks mutated the discovered response")
	}
}
