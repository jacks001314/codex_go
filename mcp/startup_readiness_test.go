package mcp

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCatalogStartupReadinessAcceptsFilteredVisibleCachedCatalog(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			Command:          "codex-catalog-readiness-command-must-not-run",
			Enabled:          true,
			Required:         true,
			StartupReadiness: MCPStartupReadinessCatalog,
			EnabledTools:     []string{"read", "hidden"},
		}},
	}})
	t.Cleanup(func() { _ = service.Close() })
	service.SetServer(MCPServerStatus{
		Name:  "docs",
		State: MCPServerStarting,
		Tools: []MCPToolInfo{
			{Name: "read"},
			{Name: "hidden", Meta: map[string]any{"ui": map[string]any{"visibility": []any{"app"}}}},
			{Name: "disabled"},
		},
	})

	if err := service.ValidateRequiredServers("thread-catalog"); err != nil {
		t.Fatalf("ValidateRequiredServers() error = %v", err)
	}
	response, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if response == nil || len(response.Data) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Data[0].Tools) != 1 || response.Data[0].Tools[0].Name != "read" {
		t.Fatalf("cached startup tools = %#v, want [read]", response.Data[0].Tools)
	}
	if response.Data[0].catalogSource != BindingCatalogSourceCached {
		t.Fatalf("catalog source = %q, want cached", response.Data[0].catalogSource)
	}

	binding := service.CaptureBinding(response.Data)
	if !binding.tools["docs"]["read"] || binding.tools["docs"]["hidden"] {
		t.Fatalf("binding tools = %#v", binding.tools)
	}
	if _, err := binding.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "hidden"}); !errors.Is(err, ErrInvalidMCPRequest) {
		t.Fatalf("hidden cached tool error = %v", err)
	}
	// Tool execution intentionally still requires a live connection.
	if _, err := binding.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "read"}); err == nil {
		t.Fatal("cached catalog unexpectedly executed a tool without a live connection")
	}
}

func TestCatalogStartupReadinessRequiresEligibleCachedCatalog(t *testing.T) {
	tests := []struct {
		name   string
		config ServerConfig
		tools  []MCPToolInfo
	}{
		{
			name: "empty catalog",
			config: ServerConfig{
				StartupReadiness: MCPStartupReadinessCatalog,
			},
		},
		{
			name: "filter removes every tool",
			config: ServerConfig{
				StartupReadiness: MCPStartupReadinessCatalog,
				EnabledTools:     []string{"allowed"},
			},
			tools: []MCPToolInfo{{Name: "blocked"}},
		},
		{
			name: "only hidden tools",
			config: ServerConfig{
				StartupReadiness: MCPStartupReadinessCatalog,
			},
			tools: []MCPToolInfo{{
				Name: "hidden",
				Meta: map[string]any{"ui": map[string]any{"visibility": []any{"app"}}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.Command = "codex-catalog-readiness-command-must-run-and-fail"
			tt.config.Enabled = true
			tt.config.Required = true
			service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
				"docs": {Config: tt.config},
			}})
			t.Cleanup(func() { _ = service.Close() })
			service.SetServer(MCPServerStatus{Name: "docs", State: MCPServerReady, Tools: tt.tools})

			if err := service.ValidateRequiredServers("thread-catalog"); err == nil {
				t.Fatal("ValidateRequiredServers() accepted an ineligible cached catalog")
			}
		})
	}
}

func TestConnectionStartupReadinessDoesNotAcceptCachedCatalog(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			Command:  "codex-connection-readiness-command-must-run-and-fail",
			Enabled:  true,
			Required: true,
		}},
	}})
	t.Cleanup(func() { _ = service.Close() })
	service.SetServer(MCPServerStatus{Name: "docs", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "read"}}})
	if err := service.ValidateRequiredServers("thread-connection"); err == nil {
		t.Fatal("connection readiness unexpectedly accepted a cached catalog")
	}
}

func TestCatalogStartupReadinessExplicitRequirementsWaitForLiveConnection(t *testing.T) {
	if os.Getenv("MCP_CONCURRENT_FAST_HELPER") == "1" {
		runMCPHelperServer()
		return
	}
	tests := []struct {
		name            string
		registration    ServerRegistration
		requiredServers []string
		requiredPlugins []string
	}{
		{
			name: "explicit server",
			registration: ServerRegistration{Config: ServerConfig{
				StartupReadiness: MCPStartupReadinessCatalog,
			}},
			requiredServers: []string{"docs"},
		},
		{
			name: "explicit plugin",
			registration: ServerRegistration{
				Source:   string(CatalogSourceSelectedPlugin),
				PluginID: "acme/docs",
				Config: ServerConfig{
					StartupReadiness: MCPStartupReadinessCatalog,
				},
			},
			requiredPlugins: []string{"acme/docs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatalf("Executable() error = %v", err)
			}
			releaseFile := t.TempDir() + string(os.PathSeparator) + "release"
			tt.registration.Config.Command = executable
			tt.registration.Config.Args = []string{"-test.run=TestCatalogStartupReadinessExplicitRequirementsWaitForLiveConnection", "--"}
			tt.registration.Config.Env = map[string]string{
				"MCP_CONCURRENT_FAST_HELPER":   "1",
				"MCP_OPTIONAL_STARTUP_BARRIER": "1",
				"MCP_CONCURRENT_RELEASE_FILE":  releaseFile,
			}
			tt.registration.Config.Enabled = true
			tt.registration.Config.StartupTimeout = 5 * time.Second
			service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{"docs": tt.registration}})
			t.Cleanup(func() { _ = service.Close() })
			cached := MCPServerStatus{
				Name:   "docs",
				State:  MCPServerStarting,
				Tools:  []MCPToolInfo{{Name: "cached-tool"}},
				source: SourceFromRegistration(&tt.registration),
			}
			if pluginID := strings.TrimSpace(tt.registration.PluginID); pluginID != "" {
				cached.PluginID = &pluginID
			}
			service.SetServer(cached)

			done := make(chan *MCPListServerStatusResponse, 1)
			go func() {
				response, _ := service.ListStatusChecked(&MCPListServerStatusParams{
					Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
					RequiredServers:      tt.requiredServers,
					RequiredPlugins:      tt.requiredPlugins,
					NonBlockingOptional:  true,
					OptionalStartupGrace: 25 * time.Millisecond,
				})
				done <- response
			}()
			select {
			case <-done:
				t.Fatal("explicitly required MCP returned from the cached catalog")
			case <-time.After(100 * time.Millisecond):
			}
			if err := os.WriteFile(releaseFile, []byte("ready"), 0o600); err != nil {
				t.Fatalf("release required helper: %v", err)
			}
			select {
			case response := <-done:
				if response == nil || len(response.Data) != 1 || len(response.Data[0].Tools) != 1 {
					t.Fatalf("required MCP response = %#v", response)
				}
				if response.Data[0].Tools[0].Name != "echo" {
					t.Fatalf("required MCP used cached catalog: %#v", response.Data[0].Tools)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("explicitly required MCP did not finish after release")
			}
		})
	}
}

func TestMCPToolIsModelVisibleLikeRust(t *testing.T) {
	tests := []struct {
		name string
		meta any
		want bool
	}{
		{name: "no metadata", want: true},
		{name: "no visibility", meta: map[string]any{"ui": map[string]any{"resourceUri": "ui://x"}}, want: true},
		{name: "model visibility", meta: map[string]any{"ui": map[string]any{"visibility": []any{"model"}}}, want: true},
		{name: "app only", meta: map[string]any{"ui": map[string]any{"visibility": []any{"app"}}}, want: false},
		{name: "non-array visibility", meta: map[string]any{"ui": map[string]any{"visibility": "model"}}, want: true},
		{name: "codex modelVisible false", meta: map[string]any{"modelVisible": false}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpToolIsModelVisible(&MCPToolInfo{Name: "tool", Meta: tt.meta}); got != tt.want {
				t.Fatalf("mcpToolIsModelVisible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCatalogStartupReadinessRejectsMissingLiveConnectionOnExecution(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			Command:          "codex-catalog-readiness-command-must-not-run",
			Enabled:          true,
			Required:         true,
			StartupReadiness: MCPStartupReadinessCatalog,
		}},
	}})
	t.Cleanup(func() { _ = service.Close() })
	service.SetServer(MCPServerStatus{Name: "docs", State: MCPServerReady, Tools: []MCPToolInfo{{Name: "read"}}})
	binding := service.CaptureBinding(service.ConfiguredStatuses())
	_, err := binding.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "read"})
	if err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("CallTool() error = %v, want the missing stdio command", err)
	}
}
