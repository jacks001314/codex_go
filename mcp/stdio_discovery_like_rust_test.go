package mcp

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	mcpDiscoveryHelperEnv     = "MCP_STDIO_DISCOVERY_HELPER"
	mcpDiscoveryHelperModeEnv = "MCP_STDIO_DISCOVERY_MODE"
	mcpDiscoveryHelperMarkers = "MCP_STDIO_DISCOVERY_MARKERS"
)

// TestMCPDiscoverLocalStdioHandshakeRecoveryLikeRust covers Rust #57210: an
// unmarked local stdio helper that cannot finish discovery is recovered with a
// fresh legacy handshake, while a successful discovery keeps the modern
// lifecycle.
func TestMCPDiscoverLocalStdioHandshakeRecoveryLikeRust(t *testing.T) {
	if os.Getenv(mcpDiscoveryHelperEnv) == "1" {
		runMCPDiscoveryHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	markers := filepath.Join(t.TempDir(), "starts")
	tests := []struct {
		mode       string
		wantStarts int
		wantModern bool
	}{
		{mode: "legacy", wantStarts: 2},
		{mode: "legacy-init-first", wantStarts: 1},
		{mode: "legacy-reject-and-exit", wantStarts: 2},
		{mode: "legacy-reject-late-initialize", wantStarts: 2},
		{mode: "legacy-silent", wantStarts: 2},
		{mode: "legacy-versions", wantStarts: 1},
		{mode: "modern", wantStarts: 1, wantModern: true},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			if err := os.Remove(markers); err != nil && !os.IsNotExist(err) {
				t.Fatalf("Remove(%q) error = %v", markers, err)
			}
			client := newMCPStdioClient(&ServerConfig{
				Command:        executable,
				Args:           []string{"-test.run=TestMCPDiscoverLocalStdioHandshakeRecoveryLikeRust", "--"},
				ProtocolMode:   MCPProtocolDiscoverLocalStdio,
				StartupTimeout: 6 * time.Second,
				Env: map[string]string{
					mcpDiscoveryHelperEnv:     "1",
					mcpDiscoveryHelperModeEnv: test.mode,
					mcpDiscoveryHelperMarkers: markers,
				},
			})
			defer client.Close()
			var result struct {
				Tools []MCPToolInfo `json:"tools"`
			}
			if err := client.Call("tools/list", map[string]any{}, &result); err != nil {
				t.Fatalf("Call() error = %v", err)
			}
			if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
				t.Fatalf("tools = %#v", result.Tools)
			}
			if starts := mcpDiscoveryHelperStarts(t, markers); starts != test.wantStarts {
				t.Fatalf("helper starts = %d, want %d", starts, test.wantStarts)
			}
			modern := client.protocolModeSnapshot() == MCPProtocol20260728
			if modern != test.wantModern {
				t.Fatalf("negotiated modern = %t, want %t", modern, test.wantModern)
			}
			if !test.wantModern && client.messageLimitSnapshot() != maxModernMCPMessageBytes {
				t.Fatalf("message limit = %d, want the bounded discovery limit", client.messageLimitSnapshot())
			}
		})
	}
}

// TestMCPDiscoverLocalStdioRejectsAuthenticationFailureWithoutRestart pins that
// an authentication failure is reported instead of being retried against a
// fresh helper.
func TestMCPDiscoverLocalStdioRejectsAuthenticationFailureWithoutRestart(t *testing.T) {
	if os.Getenv(mcpDiscoveryHelperEnv) == "1" {
		runMCPDiscoveryHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	markers := filepath.Join(t.TempDir(), "starts")
	client := newMCPStdioClient(&ServerConfig{
		Command:      executable,
		Args:         []string{"-test.run=TestMCPDiscoverLocalStdioRejectsAuthenticationFailureWithoutRestart", "--"},
		ProtocolMode: MCPProtocolDiscoverLocalStdio,
		Env: map[string]string{
			mcpDiscoveryHelperEnv:     "1",
			mcpDiscoveryHelperModeEnv: "auth",
			mcpDiscoveryHelperMarkers: markers,
		},
	})
	defer client.Close()
	var result struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	err = client.Call("tools/list", map[string]any{}, &result)
	if err == nil {
		t.Fatal("Call() error = nil, want an authentication failure")
	}
	if !isMCPAuthenticationFailure(err) {
		t.Fatalf("error = %v, want an authentication failure", err)
	}
	if starts := mcpDiscoveryHelperStarts(t, markers); starts != 1 {
		t.Fatalf("helper starts = %d, want 1 (no restart)", starts)
	}
}

// TestMCPDiscoverLocalStdioRetainsLegacyConnectionLikeRust covers Rust #57210's
// legacy retention: when enabling discovery for the same helper fails, the
// working legacy connection keeps serving the chat.
func TestMCPDiscoverLocalStdioRetainsLegacyConnectionLikeRust(t *testing.T) {
	if os.Getenv(mcpDiscoveryHelperEnv) == "1" {
		runMCPDiscoveryHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	markers := filepath.Join(t.TempDir(), "starts")
	env := map[string]string{
		mcpDiscoveryHelperEnv:     "1",
		mcpDiscoveryHelperModeEnv: "legacy-then-broken",
		mcpDiscoveryHelperMarkers: markers,
	}
	service := NewMCPService(nil)
	legacyConfig := &ServerConfig{
		Command:        executable,
		Args:           []string{"-test.run=TestMCPDiscoverLocalStdioRetainsLegacyConnectionLikeRust", "--"},
		ProtocolMode:   MCPProtocolLegacy,
		StartupTimeout: 5 * time.Second,
		Env:            env,
	}
	legacyClient := service.stdioClientForServer("docs", legacyConfig)
	defer service.Close()
	var legacyResult struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := legacyClient.Call("tools/list", map[string]any{}, &legacyResult); err != nil {
		t.Fatalf("legacy Call() error = %v", err)
	}

	discoverConfig := *legacyConfig
	discoverConfig.ProtocolMode = MCPProtocolDiscoverLocalStdio
	upgraded := service.stdioClientForServer("docs", &discoverConfig)
	if upgraded == legacyClient {
		t.Fatal("discovery-enabled config reused the legacy connection")
	}
	if upgraded.legacyFallback != legacyClient {
		t.Fatal("legacy connection was not retained for a failed upgrade")
	}
	var upgradedResult struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := upgraded.Call("tools/list", map[string]any{}, &upgradedResult); err != nil {
		t.Fatalf("upgraded Call() error = %v", err)
	}
	if len(upgradedResult.Tools) != 1 || upgradedResult.Tools[0].Name != "echo" {
		t.Fatalf("upgraded tools = %#v", upgradedResult.Tools)
	}
	if got := upgraded.protocolModeSnapshot(); got != MCPProtocolLegacy {
		t.Fatalf("delegated protocol mode = %d, want legacy", got)
	}
	if starts := mcpDiscoveryHelperStarts(t, markers); starts < 2 {
		t.Fatalf("helper starts = %d, want the failed upgrade attempt", starts)
	}
}

func TestMCPConnectionSharingGatesLocalStdioDiscoveryLikeRust(t *testing.T) {
	values := map[string]any{
		"features": map[string]any{"mcp_connection_sharing": true},
		"mcp_servers": map[string]any{
			"docs": map[string]any{"command": "helper"},
		},
	}
	runtime := RuntimeConfigFromValues(values, "")
	if !runtime.DiscoverLocalStdio {
		t.Fatal("mcp_connection_sharing did not enable local stdio discovery")
	}
	config, ok := NewMCPService(runtime).serverConfig("docs")
	if !ok || config.ProtocolMode != MCPProtocolDiscoverLocalStdio {
		t.Fatalf("docs config = %#v, ok=%v, want discovery mode", config, ok)
	}

	disabled := RuntimeConfigFromValues(map[string]any{
		"mcp_servers": map[string]any{"docs": map[string]any{"command": "helper"}},
	}, "")
	if disabled.DiscoverLocalStdio {
		t.Fatal("discovery is enabled by default")
	}
	config, ok = NewMCPService(disabled).serverConfig("docs")
	if !ok || config.ProtocolMode != MCPProtocolLegacy {
		t.Fatalf("disabled docs config = %#v, ok=%v, want legacy", config, ok)
	}

	// A helper that declares the modern version keeps that handshake even under
	// discovery, and an unknown declaration is not discoverable at all.
	marked := ServerConfig{Command: "helper", ProtocolMode: MCPProtocolDiscoverLocalStdio, Env: map[string]string{mcpProtocolVersionEnvVar: modernMCPProtocol}}
	if handshake, _, _, err := mcpStdioLaunchConfig(&marked); err != nil || handshake != MCPProtocol20260728 {
		t.Fatalf("marked discovery handshake = %d, err = %v, want modern", handshake, err)
	}
	invalid := ServerConfig{Command: "helper", ProtocolMode: MCPProtocolDiscoverLocalStdio, Env: map[string]string{mcpProtocolVersionEnvVar: "1999-01-01"}}
	if _, _, _, err := mcpStdioLaunchConfig(&invalid); err == nil {
		t.Fatal("unknown declared version was accepted for discovery")
	}
	if mcpServerCanDiscoverLocalStdio("docs", &invalid) {
		t.Fatal("invalid declared version qualified for discovery")
	}

	for name, config := range map[string]*ServerConfig{
		"remote":       {URL: "https://example.test/mcp"},
		"ema":          {Command: "helper", Auth: ServerAuthEMAAuth},
		"runtime auth": {Command: "helper", ApplyHTTPRequest: func(*http.Request, []byte) error { return nil }},
	} {
		if mcpServerCanDiscoverLocalStdio("docs", config) {
			t.Fatalf("%s config qualified for discovery", name)
		}
	}
	if mcpServerCanDiscoverLocalStdio(CodexAppsServerName, &ServerConfig{Command: "helper"}) {
		t.Fatal("the host-owned apps server qualified for discovery")
	}
}

// TestMCPDiscoverLocalStdioKeepsLegacyCatalogIdentity pins Rust #57210's catalog
// separation: discovery shares the legacy catalog identity until it negotiates
// the modern protocol, while an explicit modern connection keeps its own.
func TestMCPDiscoverLocalStdioKeepsLegacyCatalogIdentity(t *testing.T) {
	base := ServerConfig{Command: "helper"}
	legacyKey, legacyEligible := mcpToolCatalogGraceKey("docs", &base, false, false, false)
	discover := base
	discover.ProtocolMode = MCPProtocolDiscoverLocalStdio
	discoverKey, discoverEligible := mcpToolCatalogGraceKey("docs", &discover, false, false, false)
	modern := base
	modern.ProtocolMode = MCPProtocol20260728
	modernKey, _ := mcpToolCatalogGraceKey("docs", &modern, false, false, false)
	if !legacyEligible || !discoverEligible {
		t.Fatal("stdio catalog identity is not eligible")
	}
	if legacyKey != discoverKey {
		t.Fatal("discovery did not share the legacy catalog identity")
	}
	if legacyKey == modernKey {
		t.Fatal("discovery shared the explicit modern catalog identity")
	}
	if modernMCPMessageLimit(MCPProtocolDiscoverLocalStdio) != maxModernMCPMessageBytes {
		t.Fatal("discovery does not use the bounded modern message limit")
	}
	params, _ := mcpParamsWithProtocolMetadataAndFeatures(nil, MCPProtocolDiscoverLocalStdio, false, false, false).(map[string]any)
	meta, _ := params["_meta"].(map[string]any)
	if meta[mcpProtocolVersionMetadataKey] != modernMCPProtocol {
		t.Fatalf("discovery metadata = %#v, want the modern protocol version", meta)
	}
}

func runMCPDiscoveryHelper(t *testing.T) {
	t.Helper()
	mode := os.Getenv(mcpDiscoveryHelperModeEnv)
	if _, ok := os.LookupEnv(mcpProtocolVersionEnvVar); ok {
		t.Fatalf("%s leaked into child process", mcpProtocolVersionEnvVar)
	}
	markers := os.Getenv(mcpDiscoveryHelperMarkers)
	if markers != "" {
		file, err := os.OpenFile(markers, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("OpenFile(%q) error = %v", markers, err)
		}
		_, _ = file.WriteString("start\n")
		_ = file.Close()
	}
	if mode == "legacy-then-broken" && mcpDiscoveryHelperStarts(t, markers) > 1 {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	requestIndex := 0
	initialized := false
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(data, &request) != nil || request.Method == "" {
			continue
		}
		index := requestIndex
		requestIndex++
		method := request.Method
		if mode == "legacy-init-first" && !initialized {
			method = "initialize"
		}
		reply := func(result any) {
			if err := writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
				t.Fatalf("write response error = %v", err)
			}
		}
		fail := func(code int, message string, data map[string]any) {
			failure := map[string]any{"code": code, "message": message}
			if data != nil {
				failure["data"] = data
			}
			if err := writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": failure}); err != nil {
				t.Fatalf("write error response error = %v", err)
			}
		}
		switch {
		case method == "server/discover" && (mode == "legacy" || mode == "legacy-then-broken"):
			return
		case method == "server/discover" && mode == "legacy-silent":
			if err := writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info", "data": "starting server"}}); err != nil {
				t.Fatalf("write notification error = %v", err)
			}
			continue
		case method == "server/discover" && mode == "legacy-versions":
			fail(-32022, "unsupported protocol", map[string]any{"supported": []string{defaultMCPProtocol}})
		case method == "server/discover" && mode == "auth":
			fail(401, "unauthorized", nil)
			return
		case method == "server/discover" && strings.HasPrefix(mode, "legacy"):
			fail(-32601, "method not found", nil)
			if mode == "legacy-reject-and-exit" {
				return
			}
		case method == "server/discover":
			mcpDiscoveryHelperAssertModernMeta(t, request.Params)
			initialized = true
			reply(map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{modernMCPProtocol},
				"capabilities":      map[string]any{"tools": map[string]any{}},
			})
		case method == "initialize":
			if mode == "legacy-reject-late-initialize" && index != 0 {
				fail(-32600, "initialize must be the first request", nil)
				continue
			}
			initialized = true
			reply(map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "discovery-helper", "version": "1.0.0"},
			})
		case method == "notifications/initialized":
		case method == "tools/list":
			if !strings.HasPrefix(mode, "legacy") {
				mcpDiscoveryHelperAssertModernMeta(t, request.Params)
			}
			reply(map[string]any{"tools": []any{map[string]any{
				"name":        "echo",
				"description": "echo a value",
				"inputSchema": map[string]any{"type": "object"},
			}}})
		case method == "tools/call":
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
		default:
			t.Fatalf("unexpected method %q", method)
		}
	}
}

func mcpDiscoveryHelperAssertModernMeta(t *testing.T, params map[string]any) {
	t.Helper()
	meta, _ := params["_meta"].(map[string]any)
	if meta[mcpProtocolVersionMetadataKey] != modernMCPProtocol {
		t.Fatalf("protocol metadata = %#v, want %s", meta, modernMCPProtocol)
	}
}

func mcpDiscoveryHelperStarts(t *testing.T, path string) int {
	t.Helper()
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(data)))
}
