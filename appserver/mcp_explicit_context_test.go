package appserver

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"codex_go/config"
	"codex_go/execserver"
	"codex_go/mcp"
	"codex_go/plugin"
)

func TestMCPExplicitDisabledPluginsOverrideThreadProjection(t *testing.T) {
	plugins := plugin.NewPluginService()
	for _, name := range []string{"alpha", "beta"} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"`+name+`":{"type":"http","url":"https://example.test/mcp"}}}`), 0600); err != nil {
			t.Fatal(err)
		}
		plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{ID: name + "@local", Name: name, MarketplaceName: "local", Installed: true, Enabled: true, Source: plugin.PluginSource{Type: "local", Path: root}}, ManifestPath: filepath.Join(root, ".codex-plugin", "plugin.json")})
	}
	router := NewRuntimeRouter(RuntimeServices{Plugins: plugins})
	ids := []string{"alpha@local"}
	if _, err := router.services.ThreadExtras.UpdateSettings(&SettingsUpdateParams{ThreadID: "parent", DisabledPluginIDs: &ids}); err != nil {
		t.Fatal(err)
	}
	base := mcp.NewMCPServerContributionContext("parent", nil)
	for _, tc := range []struct {
		name        string
		context     mcp.MCPServerContributionContext
		alpha, beta bool
	}{
		{"inherited", base, false, true},
		{"explicit", base.WithDisabledPluginIDs([]string{"beta@local"}), true, false},
		{"clear", base.WithDisabledPluginIDs(nil), true, true},
		{"host", mcp.NewHostMCPServerContributionContext("parent", "desktop", "exec"), true, true},
		{"host restricted", mcp.NewHostMCPServerContributionContext("parent", "desktop", "exec").WithDisabledPluginIDs([]string{"beta@local"}), true, false},
		{"unrelated", mcp.NewMCPServerContributionContext("other", nil), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := router.runtimeMCPConfigForThread(tc.context, map[string]any{}, t.TempDir(), nil, nil)
			_, alpha := result.Servers["alpha"]
			_, beta := result.Servers["beta"]
			if alpha != tc.alpha || beta != tc.beta {
				t.Fatalf("servers=%v, want alpha=%v beta=%v", result.Servers, tc.alpha, tc.beta)
			}
		})
	}

	// A complete empty live executor inventory supersedes host plugins for a
	// normal step, but must not leak into host-only projection.
	snapshots := []execserver.ExecutorCapabilityDiscoveryV2Snapshot{}
	if err := router.threads.RegisterTurn("parent", "live", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	if !router.threads.UpdateTurn("parent", "live", func(active *activeRuntimeTurn) {
		active.StepCapabilities = &runtimeStepCapabilitySnapshot{discoveryV2: &snapshots}
	}) {
		t.Fatal("active turn missing")
	}
	live := router.runtimeMCPConfigForThread(base.WithDisabledPluginIDs(nil), map[string]any{}, t.TempDir(), nil, nil)
	if len(live.Servers) != 0 {
		t.Fatalf("live empty inventory ignored: %v", live.Servers)
	}
	host := router.runtimeMCPConfigForThread(mcp.NewHostMCPServerContributionContext("parent", "desktop", "exec"), map[string]any{}, t.TempDir(), nil, nil)
	if len(host.Servers) != 2 {
		t.Fatalf("host inherited live inventory: %v", host.Servers)
	}
	if got := plugins.EnabledMCPServerContributions(); len(got) != 2 {
		t.Fatalf("projection changed shared plugins: %v", got)
	}
}

func TestMCPExplicitOriginatorOnlyChangesCompatibilityApps(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	values := map[string]any{"mcp_servers": map[string]any{"external": map[string]any{"url": "https://example.test/mcp", "http_headers": map[string]any{"originator": "configured"}}}}
	runtimeAuth := &mcp.RuntimeAuth{UsesCodexBackend: true}
	base := mcp.NewMCPServerContributionContext("parent", nil)
	for _, originator := range []string{"codex_work_desktop", "guardian", ""} {
		result := router.runtimeMCPConfigForThread(base.WithOriginator(originator), values, t.TempDir(), runtimeAuth, nil)
		apps, ok := result.Servers[mcp.CodexAppsServerName]
		if !ok {
			t.Fatal("implicit Apps registration missing")
		}
		if got := apps.Config.HTTPHeaders["originator"]; got != originator {
			t.Fatalf("Apps originator=%q want %q", got, originator)
		}
		if got := result.Servers["external"].Config.HTTPHeaders["originator"]; got != "configured" {
			t.Fatalf("external header changed: %q", got)
		}
	}
}

func TestMCPHostContextEnvironmentReadiness(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	values := map[string]any{"mcp_servers": map[string]any{"remote": map[string]any{"url": "https://example.test/mcp", "environment_id": "env-a"}}}
	for _, state := range []mcp.EnvironmentSelectionState{mcp.EnvironmentSelectionReady, mcp.EnvironmentSelectionPending, mcp.EnvironmentSelectionFailed} {
		context := mcp.NewHostMCPServerContributionContext("parent", "desktop", "exec").WithSelectedEnvironments(mcp.NewSelectedEnvironments([]mcp.TurnEnvironmentSelection{{EnvironmentID: "env-a", State: state}}))
		result := router.runtimeMCPConfigForThread(context, values, t.TempDir(), nil, nil)
		registration, ok := result.Servers["remote"]
		if !ok {
			t.Fatal("registration diagnostics lost")
		}
		want := state == mcp.EnvironmentSelectionReady
		if registration.Config.Enabled != want {
			t.Fatalf("state %s enabled=%v want %v", state, registration.Config.Enabled, want)
		}
		_, configured := mcp.NewManager(nil).ConfiguredServers(result)["remote"]
		if configured != want {
			t.Fatalf("state %s configured=%v", state, configured)
		}
		if result.EnvironmentAuthority == nil {
			t.Fatal("authority not captured")
		}
	}
}

type mcpOriginatorCaptureTransport struct {
	mu     sync.Mutex
	values []string
}

func (c *mcpOriginatorCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.values = append(c.values, request.Header.Get("originator"))
	c.mu.Unlock()
	return nil, errors.New("intentional transport stop after header capture")
}

func TestMCPExplicitOriginatorReachesHTTPTransport(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	context := mcp.NewHostMCPServerContributionContext("parent", "codex_work_desktop", "exec")
	config := router.runtimeMCPConfigForThread(context, map[string]any{}, t.TempDir(), &mcp.RuntimeAuth{UsesCodexBackend: true}, nil)
	capture := &mcpOriginatorCaptureTransport{}
	config.HTTPClient = &http.Client{Transport: capture}
	service := mcp.NewMCPService(config)
	defer service.Close()
	// The transport deliberately stops before network I/O. Initialization still
	// constructs the real MCP request and applies the projected headers.
	_, _ = service.CallTool(&mcp.MCPToolCallParams{ThreadID: "parent", ServerName: mcp.CodexAppsServerName, ToolName: "probe", Arguments: map[string]any{}})
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.values) == 0 {
		t.Fatal("MCP never reached HTTP transport")
	}
	for _, got := range capture.values {
		if got != "codex_work_desktop" {
			t.Fatalf("HTTP originator=%q", got)
		}
	}
}

func TestMCPHostEmptySelectionRestrictsRemoteRegistrations(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	values := map[string]any{"mcp_servers": map[string]any{
		"remote": map[string]any{"url": "https://example.test/mcp", "environment_id": "env-a"},
		"local":  map[string]any{"url": "https://example.test/local"},
	}}
	base := mcp.NewHostMCPServerContributionContext("parent", "desktop", "exec")
	for _, explicit := range []bool{false, true} {
		context := base
		if explicit {
			context = context.WithSelectedEnvironments(mcp.NewSelectedEnvironments(nil))
		}
		result := router.runtimeMCPConfigForThread(context, values, t.TempDir(), nil, nil)
		if got := result.Servers["remote"].Config.Enabled; got == explicit {
			t.Fatalf("explicit empty=%v remote enabled=%v", explicit, got)
		}
		if !result.Servers["local"].Config.Enabled {
			t.Fatal("default environment was disabled")
		}
	}
}

func TestMCPHostReadyEnvironmentHonorsOwnerDenyPolicy(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	values := map[string]any{"mcp_servers": map[string]any{"remote": map[string]any{"url": "https://example.test/mcp", "environment_id": "env-a"}}}
	for _, deny := range []bool{false, true} {
		policy := &config.EnvironmentMCPPolicy{}
		if deny {
			policy.MCPServers = map[string]config.MCPServerRequirement{}
		}
		context := mcp.NewHostMCPServerContributionContext("parent", "desktop", "exec").WithSelectedEnvironments(mcp.NewSelectedEnvironments([]mcp.TurnEnvironmentSelection{{EnvironmentID: "env-a", State: mcp.EnvironmentSelectionReady, McpPolicy: policy}}))
		result := router.runtimeMCPConfigForThread(context, values, t.TempDir(), nil, nil)
		if got := result.Servers["remote"].Config.Enabled; got == deny {
			t.Fatalf("deny=%v enabled=%v", deny, got)
		}
	}
}
