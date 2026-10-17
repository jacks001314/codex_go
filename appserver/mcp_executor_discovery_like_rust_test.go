package appserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/mcp"
	"codex_go/turn"
)

func TestExecutorHTTPMCPServersFiltersExecutorConfigLikeRust(t *testing.T) {
	hostname := "executor.example"
	response := &execserverclient.EnvironmentConfigReadResponse{
		Hostname: &hostname,
		Config: execserverclient.EnvironmentConfigLayerStack{Layers: []execserverclient.EnvironmentConfigLayer{
			{TOML: `
[mcp_servers.node_repl]
url = "http://executor-only.invalid/mcp"
http_headers = { Authorization = "Bearer executor-browser-token" }
startup_timeout_sec = 10

[mcp_servers.ignored_stdio]
command = "executor-local-command"

[mcp_servers.env_headers]
url = "http://executor-only.invalid/env"
env_http_headers = { Authorization = "TOKEN" }

[mcp_servers.header_helper]
url = "http://executor-only.invalid/helper"
http_headers_helper = "helper"

[mcp_servers.bearer_env]
url = "http://executor-only.invalid/bearer"
bearer_token_env_var = "TOKEN"

[mcp_servers.foreign]
url = "http://executor-only.invalid/foreign"
environment_id = "another-environment"
`},
			{TOML: `
[mcp_servers.policy_unlisted]
url = "http://executor-only.invalid/unlisted"
`},
		}},
		Requirements: execserverclient.EnvironmentConfigLayerStack{Layers: []execserverclient.EnvironmentConfigLayer{
			{TOML: `
[mcp_servers.node_repl.identity]
url = "http://executor-only.invalid/mcp"
`},
		}},
	}
	servers, err := executorHTTPMCPServers(response, "executor-1", executorHTTPDoer{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]mcp.ServerConfig{}
	for _, server := range servers {
		byName[server.name] = server.config
	}
	if len(byName) != 2 {
		t.Fatalf("discovered servers = %#v, want node_repl and policy_unlisted", byName)
	}
	nodeRepl := byName["node_repl"]
	if nodeRepl.EnvironmentID != "executor-1" || nodeRepl.Required || !nodeRepl.Enabled ||
		nodeRepl.CredentialPolicy != mcp.CredentialPolicyExecutorOnly ||
		nodeRepl.HTTPHeaders["Authorization"] != "Bearer executor-browser-token" {
		t.Fatalf("node_repl = %#v", nodeRepl)
	}
	if nodeRepl.HTTPClient == nil {
		t.Fatal("node_repl did not bind an executor HTTP client")
	}
	unlisted := byName["policy_unlisted"]
	if unlisted.Enabled || unlisted.DisabledReason != config.MCPDisabledByRequirements {
		t.Fatalf("policy_unlisted = %#v, want requirements veto", unlisted)
	}
}

func TestExecutorHTTPDoerRoutesThroughExecServerLikeRust(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer executor-token" || string(body) != "payload" {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("X-Executor", "yes")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("executor response"))
	}))
	defer upstream.Close()
	server, client := startExecServerTestClient(t)
	defer server.Close()
	defer client.Close()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, upstream.URL, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer executor-token")
	response, err := (executorHTTPDoer{client: client}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || response.Header.Get("X-Executor") != "yes" || string(body) != "executor response" {
		t.Fatalf("executor response = %d %q %#v", response.StatusCode, body, response.Header)
	}
}

func TestExecutorMCPProjectionPreservesExistingOwnershipLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "file:///workspace")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "executor-1", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	record, ok := manager.Record("executor-1")
	if !ok || record == nil {
		t.Fatal("executor record missing")
	}
	selection := map[string]any{"environmentId": "executor-1", "cwd": "file:///workspace"}
	key, err := executorMCPDiscoveryKey(*record, "file:///workspace", selection)
	if err != nil {
		t.Fatal(err)
	}
	cache := newRuntimeCapabilityDiscoveryV2Cache()
	cache.storeExecutorMCPDiscovery("executor-1", executorMCPDiscoveryResult{
		key: key,
		servers: []executorMCPServer{
			{name: "plugin", config: mcp.ServerConfig{URL: "http://executor/plugin", Enabled: true}},
			{name: "configured", config: mcp.ServerConfig{URL: "http://executor/configured", Enabled: true}},
			{name: "foreign", config: mcp.ServerConfig{URL: "http://executor/foreign", Enabled: true}},
			{name: "disabled_plugin", config: mcp.ServerConfig{URL: "http://executor/disabled", Enabled: false}},
		},
	})
	runtimeConfig := &mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"plugin": {
			Name: "plugin", Source: string(mcp.CatalogSourcePlugin), PluginID: "plugin@1",
			Config: mcp.ServerConfig{URL: "http://host/plugin", EnvironmentID: "executor-1", Enabled: true},
		},
		"configured": {
			Name: "configured", Source: string(mcp.CatalogSourceConfig),
			Config: mcp.ServerConfig{URL: "http://host/configured", EnvironmentID: "executor-1", Enabled: true},
		},
		"foreign": {
			Name: "foreign", Source: string(mcp.CatalogSourceConfig),
			Config: mcp.ServerConfig{URL: "http://host/foreign", EnvironmentID: "executor-2", Enabled: true},
		},
	}}
	router := &RuntimeRouter{services: RuntimeServices{Environment: manager}}
	cache.applyExecutorMCPServers(router, runtimeConfig, []map[string]any{selection}, nil)

	if got := runtimeConfig.Servers["plugin"]; got.Config.URL != "http://executor/plugin" || mcp.SourceFromRegistration(&got) != mcp.CatalogSourceConfig {
		t.Fatalf("same-environment plugin was not supplemented: %#v", got)
	}
	if got := runtimeConfig.Servers["configured"]; got.Config.URL != "http://host/configured" {
		t.Fatalf("host-configured server was replaced: %#v", got)
	}
	if got := runtimeConfig.Servers["foreign"]; got.Config.URL != "http://host/foreign" || got.Config.EnvironmentID != "executor-2" {
		t.Fatalf("foreign-environment server was replaced: %#v", got)
	}
	if got := runtimeConfig.Servers["disabled_plugin"]; got.Config.Enabled || got.Config.EnvironmentID != "executor-1" {
		t.Fatalf("disabled executor plugin = %#v", got)
	}
}

func TestSelectedExecutorProjectsHTTPMCPServersLikeRust(t *testing.T) {
	reader := &mcpExecutorDiscoveryTestReader{response: &execserverclient.EnvironmentConfigReadResponse{
		Config: execserverclient.EnvironmentConfigLayerStack{Layers: []execserverclient.EnvironmentConfigLayer{
			{TOML: `
[mcp_servers.node_repl]
url = "http://executor-only.invalid/mcp"
http_headers = { Authorization = "Bearer executor-browser-token" }
startup_timeout_sec = 10

[mcp_servers.ignored_stdio]
command = "executor-local-command"

[mcp_servers.policy_unlisted]
url = "http://executor-only.invalid/unlisted"
`},
		}},
	}}
	execServerURL, closeExecServer := startMCPDiscoveryExecServer(t, reader)
	defer closeExecServer()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "file:///workspace")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "executor-1", ExecServerURL: execServerURL}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:      config.NewConfigService(t.TempDir()),
		Environment: manager,
	})
	defer router.Close()
	nodeReplURL := "http://executor-only.invalid/mcp"
	cfg := &config.Config{
		Values: map[string]any{},
		Requirements: &config.ConfigRequirements{MCPServers: map[string]config.MCPServerRequirement{
			"node_repl": {Identity: &config.MCPServerIdentity{URL: &nodeReplURL}},
		}},
	}
	params := &turn.TurnStartParams{
		ThreadID: "thread-executor-mcp",
		Environments: []map[string]any{{
			"environmentId": "executor-1",
			"cwd":           "file:///workspace",
		}},
	}
	if err := router.registerActiveRuntimeTurn(params.ThreadID, "turn-executor-mcp", nil, 1, params); err != nil {
		t.Fatal(err)
	}
	service := router.mcpServiceForThread(params.ThreadID, cfg)
	if service == nil {
		t.Fatal("managed MCP service = nil")
	}
	nodeRepl, ok := service.ServerConfigForServer("node_repl")
	if !ok || !nodeRepl.Enabled || nodeRepl.EnvironmentID != "executor-1" || nodeRepl.HTTPClient == nil {
		t.Fatalf("node_repl = %#v, ok=%t", nodeRepl, ok)
	}
	if _, ok := service.ServerConfigForServer("ignored_stdio"); ok {
		t.Fatal("executor-local stdio server was projected")
	}
	if unlisted, ok := service.ServerConfigForServer("policy_unlisted"); ok {
		t.Fatalf("policy_unlisted became available: %#v", unlisted)
	}
	if !reader.called {
		t.Fatal("executor environmentConfig/read was not called")
	}
}

func TestSelectedExecutorHTTPMCPActivatesWithoutHostMCPConfigLikeRust(t *testing.T) {
	reader := &mcpExecutorDiscoveryTestReader{response: &execserverclient.EnvironmentConfigReadResponse{
		Config: execserverclient.EnvironmentConfigLayerStack{Layers: []execserverclient.EnvironmentConfigLayer{
			{TOML: `
[mcp_servers.node_repl]
url = "http://executor-only.invalid/mcp"
`},
		}},
	}}
	execServerURL, closeExecServer := startMCPDiscoveryExecServer(t, reader)
	defer closeExecServer()
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "file:///workspace")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "executor-1", ExecServerURL: execServerURL}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:      config.NewConfigService(t.TempDir()),
		Environment: manager,
	})
	defer router.Close()
	params := &turn.TurnStartParams{
		ThreadID: "thread-executor-mcp-no-host-config",
		Environments: []map[string]any{{
			"environmentId": "executor-1",
			"cwd":           "file:///workspace",
		}},
	}
	if err := router.registerActiveRuntimeTurn(params.ThreadID, "turn-executor-mcp-no-host-config", nil, 1, params); err != nil {
		t.Fatal(err)
	}
	service := router.mcpServiceForThread(params.ThreadID, &config.Config{Values: map[string]any{}})
	if service == nil {
		t.Fatal("managed MCP service = nil")
	}
	nodeRepl, ok := service.ServerConfigForServer("node_repl")
	if !ok || !nodeRepl.Enabled || nodeRepl.EnvironmentID != "executor-1" || nodeRepl.HTTPClient == nil {
		t.Fatalf("node_repl = %#v, ok=%t", nodeRepl, ok)
	}
}

type mcpExecutorDiscoveryTestReader struct {
	response *execserverclient.EnvironmentConfigReadResponse
	called   bool
}

func (r *mcpExecutorDiscoveryTestReader) ReadEnvironmentConfig(params *execserverclient.EnvironmentConfigReadParams, preferMXC *bool) (*execserverclient.EnvironmentConfigReadResponse, error) {
	r.called = true
	return r.response, nil
}

func startMCPDiscoveryExecServer(t *testing.T, reader execserverclient.EnvironmentConfigReader) (string, func()) {
	t.Helper()
	serverCtx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	server := execserverclient.NewServer()
	server.SetEnvironmentConfigReader(reader)
	go func() {
		serverDone <- server.ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &appServerExecServerURLWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("exec-server URL was not reported")
	}
	return serverURL, func() {
		cancel()
		<-serverDone
	}
}
