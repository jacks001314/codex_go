package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/plugin"
)

// Rust #62112: only the exact bundled Memory SessionStart declaration is a
// built-in App hook, and it still names the Dream Memory connector.
func TestBundledMemoryHookAppConnectorIDLikeRust(t *testing.T) {
	exactMatcher := bundledMemoryHookMatcher
	otherMatcher := ".*"
	for _, tc := range []struct {
		name     string
		pluginID string
		event    HookEventName
		matcher  *string
		handler  hookJSONHandlerConfigWire
		want     string
	}{
		{
			name:     "exact declaration",
			pluginID: bundledMemoryHookPluginID,
			event:    HookEventSessionStart,
			matcher:  &exactMatcher,
			handler:  bundledHookHandler("codex_apps", "codex_dream_memory.get_mec", map[string]any{}),
			want:     bundledMemoryHookConnectorID,
		},
		{
			name:     "match-all matcher is not trusted",
			pluginID: bundledMemoryHookPluginID,
			event:    HookEventSessionStart,
			matcher:  &otherMatcher,
			handler:  bundledHookHandler("codex_apps", "codex_dream_memory.get_mec", map[string]any{}),
		},
		{
			name:     "manifest-provided input is not trusted",
			pluginID: bundledMemoryHookPluginID,
			event:    HookEventSessionStart,
			matcher:  &exactMatcher,
			handler:  bundledHookHandler("codex_apps", "codex_dream_memory.get_mec", map[string]any{"untrusted": true}),
		},
		{
			name:     "missing matcher is not trusted",
			pluginID: bundledMemoryHookPluginID,
			event:    HookEventSessionStart,
			handler:  bundledHookHandler("codex_apps", "codex_dream_memory.get_mec", map[string]any{}),
		},
		{
			name:     "other plugin is not trusted",
			pluginID: "browser@openai-bundled",
			event:    HookEventSessionStart,
			matcher:  &exactMatcher,
			handler:  bundledHookHandler("codex_apps", "codex_dream_memory.get_mec", map[string]any{}),
		},
		{
			name:     "other server is not trusted",
			pluginID: bundledMemoryHookPluginID,
			event:    HookEventSessionStart,
			matcher:  &exactMatcher,
			handler:  bundledHookHandler("node_repl", "codex_dream_memory.get_mec", map[string]any{}),
		},
		{
			name:     "command handler is not an App hook",
			pluginID: bundledMemoryHookPluginID,
			event:    HookEventSessionStart,
			matcher:  &exactMatcher,
			handler:  hookJSONHandlerConfigWire{Type: string(HookHandlerCommand), Command: "true"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundledHookAppConnectorID(tc.pluginID, tc.event, tc.matcher, tc.handler); got != tc.want {
				t.Fatalf("bundledHookAppConnectorID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Rust #62112: the exact bundled Memory hook is discovered as builtin and
// trusted without a saved hash, carries the connector for dispatch, and stays
// out of the public hooks list. Any other declaration keeps ordinary trust.
func TestBundledMemoryHookTrustRequiresExactDeclarationLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		matcher string
		input   string
		trusted bool
	}{
		{name: "exact", matcher: bundledMemoryHookMatcher, input: "{}", trusted: true},
		{name: "wildcard matcher", matcher: ".*", input: "{}"},
		{name: "untrusted input", matcher: bundledMemoryHookMatcher, input: `{"untrusted":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			hooksDir := filepath.Join(root, "hooks")
			if err := os.MkdirAll(hooksDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(hooksDir, "hooks.json")
			body := `{"hooks":{"SessionStart":[{"matcher":"` + tc.matcher + `","hooks":[{"type":"mcp_tool","server":"` +
				bundledMemoryHookServer + `","tool":"` + bundledMemoryHookTool + `","input":` + tc.input + `}]}]}}`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewHookDiscoveryService("")
			service.McpToolHooksEnabled = true
			service.PluginHookSources = []plugin.HookSource{{
				PluginID:           bundledMemoryHookPluginID,
				PluginRoot:         root,
				SourcePath:         path,
				SourceRelativePath: "hooks/hooks.json",
			}}
			discovered := service.Discover(&HookListParams{CWDs: []string{t.TempDir()}}, "")
			if len(discovered.Data) != 1 || len(discovered.Data[0].Hooks) != 1 {
				t.Fatalf("discovered hooks = %#v", discovered.Data)
			}
			hook := discovered.Data[0].Hooks[0]
			if hook.Builtin != tc.trusted {
				t.Fatalf("builtin = %v, want %v (%#v)", hook.Builtin, tc.trusted, hook)
			}
			wantTrust := HookTrustUntrusted
			if tc.trusted {
				wantTrust = HookTrustTrusted
			}
			if hook.TrustStatus != wantTrust {
				t.Fatalf("trust = %s, want %s", hook.TrustStatus, wantTrust)
			}
			if tc.trusted && !hook.Enabled {
				t.Fatalf("built-in hook is disabled: %#v", hook)
			}
			wantConnector := ""
			if tc.trusted {
				wantConnector = bundledMemoryHookConnectorID
			}
			if hook.expectedAppConnectorID != wantConnector {
				t.Fatalf("expected connector = %q, want %q", hook.expectedAppConnectorID, wantConnector)
			}
			public := hideBuiltinHooks(discovered)
			wantPublic := 1
			if tc.trusted {
				wantPublic = 0
			}
			if len(public.Data) != 1 || len(public.Data[0].Hooks) != wantPublic {
				t.Fatalf("public hooks = %#v, want %d", public.Data, wantPublic)
			}
		})
	}
}

// The built-in hook carries its connector identity into the MCP call so the
// executor can verify the live catalog (Rust #62112).
func TestBundledMemoryHookRunnerCarriesConnectorLikeRust(t *testing.T) {
	runner := NewHookRunner()
	executor := &recordingMcpToolHookExecutor{output: `{}`}
	runner.McpToolHookExecutor = executor
	server := bundledMemoryHookServer
	toolName := bundledMemoryHookTool
	matcher := bundledMemoryHookMatcher
	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:      "thread-1",
		CWD:           t.TempDir(),
		EventName:     HookEventSessionStart,
		MatcherInputs: []string{"startup"},
		Hooks: []HookMetadata{{
			Key:                    "memory-session-start",
			EventName:              HookEventSessionStart,
			HandlerType:            HookHandlerMCPTool,
			ExecutionMode:          HookExecutionSync,
			Matcher:                &matcher,
			Server:                 &server,
			Tool:                   &toolName,
			Input:                  map[string]any{},
			Builtin:                true,
			Enabled:                true,
			TrustStatus:            HookTrustTrusted,
			expectedAppConnectorID: bundledMemoryHookConnectorID,
		}},
	})
	if err != nil {
		t.Fatalf("RunStop() error = %v", err)
	}
	if !executor.called {
		t.Fatalf("MCP executor was not called: %#v", result)
	}
	if executor.expectedAppConnectorID != bundledMemoryHookConnectorID {
		t.Fatalf("expected connector = %q, want %q", executor.expectedAppConnectorID, bundledMemoryHookConnectorID)
	}
}
