package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	execserverclient "codex_go/execserver"
	"codex_go/mcp"
)

func executorHookManifest(name string, event string, server string, tool string, input map[string]any) string {
	handler := map[string]any{
		"type":   "mcp_tool",
		"server": server,
		"tool":   tool,
	}
	if input != nil {
		handler["input"] = input
	}
	manifest := map[string]any{
		"name": name,
		"hooks": map[string]any{
			"hooks": map[string]any{
				event: []any{
					map[string]any{"hooks": []any{handler}},
				},
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func executorHookSnapshot(pluginID string, environmentID string, manifest string) execserverclient.ExecutorCapabilityDiscoveryV2Snapshot {
	return execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
		EnvironmentID: environmentID,
		Response: &execserverclient.DiscoverV2CapabilitiesResponse{
			Plugins: []execserverclient.ExecutorPlugin{{
				ID:   pluginID,
				Root: "file:///plugins/" + pluginID,
				Files: execserverclient.DiscoveredPluginFiles{
					Manifest: execserverclient.CapabilityTextFile{
						Path:     "file:///plugins/" + pluginID + "/.codex-plugin/plugin.json",
						Contents: manifest,
					},
				},
			}},
		},
	}
}

func executorHookLookup(info *mcp.RuntimeToolInfo) func(string, string) *mcp.RuntimeToolInfo {
	return func(server string, tool string) *mcp.RuntimeToolInfo {
		if info == nil || server != info.ServerName || tool != info.Tool.Name {
			return nil
		}
		return info
	}
}

// Mirrors Rust #41456: an app cleanup target is admitted only when the listed
// browser tool belongs to the expected connector and carries trusted routing
// metadata. The MCP call must use the default environment, not the executor
// that supplied the plugin manifest.
func TestExecutorPluginHookSourcesAdmitAppTargetsLikeRust(t *testing.T) {
	routing := map[string]any{
		"_codex_apps": map[string]any{
			"resource_uri":        "/connector_openai_browser/browser-link/turn_ended",
			"contains_mcp_source": true,
		},
	}
	info := &mcp.RuntimeToolInfo{
		ServerName:  mcp.RuntimeCodexAppsMCPServerName,
		ConnectorID: "connector_openai_browser",
		Tool:        mcp.RuntimeTool{Name: "browser.turn_ended"},
		Meta:        routing,
	}
	for _, testCase := range []struct {
		name        string
		event       string
		connectorID string
		input       map[string]any
		meta        any
		want        bool
	}{
		{name: "registered stop target", event: "Stop", connectorID: "connector_openai_browser", input: map[string]any{}, meta: routing, want: true},
		{name: "registered subagent target", event: "SubagentStop", connectorID: "connector_openai_browser", input: map[string]any{}, meta: routing, want: true},
		{name: "colliding connector", event: "Stop", connectorID: "connector_other_browser", input: map[string]any{}, meta: routing},
		{name: "unavailable connector", event: "Stop", connectorID: "", input: map[string]any{}, meta: routing},
		{name: "manifest arguments", event: "Stop", connectorID: "connector_openai_browser", input: map[string]any{"untrusted": true}, meta: routing},
		{name: "missing routing metadata", event: "Stop", connectorID: "connector_openai_browser", input: map[string]any{}, meta: nil},
		{name: "invalid resource uri", event: "Stop", connectorID: "connector_openai_browser", input: map[string]any{}, meta: map[string]any{"_codex_apps": map[string]any{"resource_uri": 42}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lookupInfo := *info
			lookupInfo.ConnectorID = testCase.connectorID
			lookupInfo.Meta = testCase.meta
			snapshot := executorHookSnapshot(
				"browser@openai-curated-remote",
				"executor-a",
				executorHookManifest("browser", testCase.event, mcp.RuntimeCodexAppsMCPServerName, "browser.turn_ended", testCase.input),
			)
			sources := executorPluginHookSources([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{snapshot}, executorHookLookup(&lookupInfo))
			if got := len(sources) == 1; got != testCase.want {
				t.Fatalf("sources admitted = %v, want %v: %#v", got, testCase.want, sources)
			}
			if !testCase.want {
				return
			}
			targets := executorCleanupTargets(sources)
			if len(targets) != 1 {
				t.Fatalf("targets = %#v, want one", targets)
			}
			target := targets[0]
			if target.MCPEnvironmentID != mcp.DefaultMCPServerEnvironmentID {
				t.Fatalf("MCP environment = %q, want %q", target.MCPEnvironmentID, mcp.DefaultMCPServerEnvironmentID)
			}
			meta, ok := target.MCPMetadata["_codex_apps"].(map[string]any)
			if !ok || meta["resource_uri"] != routing["_codex_apps"].(map[string]any)["resource_uri"] {
				t.Fatalf("routing metadata = %#v", target.MCPMetadata)
			}
		})
	}
}

// Mirrors Rust's multi-environment registration: same cleanup targets run
// once per event and MCP environment, while distinct environments for an
// ordinary executor target each remain registered.
func TestExecutorCleanupTargetsDeduplicateByEventEnvironmentServerAndToolLikeRust(t *testing.T) {
	routing := map[string]any{"_codex_apps": map[string]any{"resource_uri": "ui://browser/turn_ended"}}
	appLooks := func() *mcp.RuntimeToolInfo {
		return &mcp.RuntimeToolInfo{
			ServerName:  mcp.RuntimeCodexAppsMCPServerName,
			ConnectorID: "connector_openai_browser",
			Tool:        mcp.RuntimeTool{Name: "browser.turn_ended"},
			Meta:        routing,
		}
	}
	appSources := executorPluginHookSources([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
		executorHookSnapshot("browser@openai-curated-remote", "executor-a", executorHookManifest("browser", "Stop", mcp.RuntimeCodexAppsMCPServerName, "browser.turn_ended", map[string]any{})),
		executorHookSnapshot("browser@openai-curated-remote", "executor-b", executorHookManifest("browser", "Stop", mcp.RuntimeCodexAppsMCPServerName, "browser.turn_ended", map[string]any{})),
	}, executorHookLookup(appLooks()))
	if len(appSources) != 2 {
		t.Fatalf("sources = %d, want two executor environments", len(appSources))
	}
	if targets := executorCleanupTargets(appSources); len(targets) != 1 {
		t.Fatalf("app targets = %#v, want one shared default-environment target", targets)
	}

	localSnapshots := []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
		executorHookSnapshot("computer-use@openai-bundled", "executor-a", executorHookManifest("computer-use", "Stop", "node_repl", "turn_ended", nil)),
		executorHookSnapshot("computer-use@openai-bundled", "executor-b", executorHookManifest("computer-use", "Stop", "node_repl", "turn_ended", nil)),
	}
	localSources := executorPluginHookSources(localSnapshots, nil)
	if targets := executorCleanupTargets(localSources); len(targets) != 2 {
		t.Fatalf("local targets = %#v, want one per executor environment", targets)
	}
}

func TestExecutorCleanupExecutionCarriesRoutingAndExpandsInputLikeRust(t *testing.T) {
	executor := &recordingMcpToolHookExecutor{}
	targets := []executorCleanupTarget{{
		Event:            HookEventStop,
		Server:           mcp.RuntimeCodexAppsMCPServerName,
		Tool:             "browser.turn_ended",
		Input:            map[string]any{"turn_id": "${turn_id}", "hook_event_name": "${hook_event_name}"},
		Timeout:          5 * time.Second,
		MCPEnvironmentID: mcp.DefaultMCPServerEnvironmentID,
		MCPMetadata:      map[string]any{"_codex_apps": map[string]any{"resource_uri": "ui://browser/turn_ended"}},
	}}
	executeExecutorCleanupTargets(context.Background(), executor, targets, HookEventStop, map[string]any{
		"turn_id":         "turn-1",
		"hook_event_name": "Stop",
	})
	if !executor.called {
		t.Fatal("cleanup target was not executed")
	}
	if executor.envID != mcp.DefaultMCPServerEnvironmentID || executor.server != mcp.RuntimeCodexAppsMCPServerName || executor.tool != "browser.turn_ended" {
		t.Fatalf("call = %q/%q/%q", executor.envID, executor.server, executor.tool)
	}
	if !reflect.DeepEqual(executor.input, map[string]any{"turn_id": "turn-1", "hook_event_name": "Stop"}) {
		t.Fatalf("expanded input = %#v", executor.input)
	}
	meta, _ := executor.meta["_codex_apps"].(map[string]any)
	if meta["resource_uri"] != "ui://browser/turn_ended" {
		t.Fatalf("metadata = %#v", executor.meta)
	}
}

// File-backed executor hooks remain unsupported until the manifest itself is
// trusted; a manifest naming a hook file must contribute no source.
func TestExecutorPluginHookSourcesIgnoreFileBackedHooksLikeRust(t *testing.T) {
	manifest := `{"name":"computer-use","hooks":"./hooks/hooks.json"}`
	if sources := executorPluginHookSources([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
		executorHookSnapshot("computer-use@openai-bundled", "executor-a", manifest),
	}, nil); len(sources) != 0 {
		t.Fatalf("sources = %#v, want none", sources)
	}
}
