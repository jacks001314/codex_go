package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestListStatusAndToolCall(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"fs": {Config: ServerConfig{Command: "mcp-fs", Enabled: true}},
	}})
	service.SetServer(MCPServerStatus{Server: MCPServerInfo{Name: "custom"}, State: MCPServerReady, Tools: []MCPToolInfo{{Name: "read"}}})
	response := service.ListStatus(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{IncludeTools: true}})
	if len(response.Servers) != 2 || response.Servers[0].Server.Name != "custom" {
		t.Fatalf("response = %#v", response)
	}

	encodedStatus, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal status returned error: %v", err)
	}
	var statusPayload map[string]any
	if err := json.Unmarshal(encodedStatus, &statusPayload); err != nil {
		t.Fatalf("Unmarshal status returned error: %v", err)
	}
	if _, ok := statusPayload["servers"]; ok {
		t.Fatalf("legacy servers key should not be emitted: %#v", statusPayload)
	}
	if _, ok := statusPayload["data"]; !ok || statusPayload["nextCursor"] != nil {
		t.Fatalf("status payload = %#v", statusPayload)
	}
	statusData := statusPayload["data"].([]any)
	firstStatus := statusData[0].(map[string]any)
	serverInfo := firstStatus["serverInfo"].(map[string]any)
	for _, internalKey := range []string{"command", "args"} {
		if _, ok := serverInfo[internalKey]; ok {
			t.Fatalf("internal serverInfo key %q should not be emitted: %#v", internalKey, serverInfo)
		}
	}
	tools := firstStatus["tools"].(map[string]any)
	readTool := tools["read"].(map[string]any)
	if schema, ok := readTool["inputSchema"].(map[string]any); !ok || len(schema) != 0 {
		t.Fatalf("tool inputSchema should be emitted as empty object: %#v", readTool)
	}
	call, err := service.CallTool(&MCPToolCallParams{ServerName: "custom", ToolName: "read", Arguments: map[string]any{"path": "a"}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if !strings.Contains(call.Content[0].Text, "path") {
		t.Fatalf("call = %#v", call)
	}
}

func TestExecutorOwnedChatGPTMCPFailsBeforeConnecting(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			URL:           "http://127.0.0.1:1/mcp",
			Auth:          ServerAuthChatGPT,
			EnvironmentID: "customer-executor",
			Enabled:       true,
		}},
	}})
	_, err := service.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "read"})
	if err == nil || err.Error() != "executor-owned MCP server `docs` cannot use hosted ChatGPT authentication; configure executor-owned credentials instead" {
		t.Fatalf("CallTool() error = %v", err)
	}
	if status := service.authStatusForConfig("docs", &ServerConfig{URL: "https://example.test/mcp", Auth: ServerAuthChatGPT, EnvironmentID: "customer-executor"}); status != MCPAuthUnsupported {
		t.Fatalf("unsafe remote ChatGPT auth status = %s, want %s", status, MCPAuthUnsupported)
	}
	if status := service.authStatusForConfig("docs", &ServerConfig{URL: "https://example.test/mcp", Auth: ServerAuthChatGPT, EnvironmentID: "customer-executor", HTTPHeaders: map[string]string{"Authorization": "Bearer executor"}}); status != MCPAuthBearerToken {
		t.Fatalf("safe remote ChatGPT auth status = %s, want %s", status, MCPAuthBearerToken)
	}
}

func TestMCPServerStatusPreservesRawServerAndToolNames(t *testing.T) {
	service := NewMCPService(nil)
	title := "Lookup Server"
	service.SetServer(MCPServerStatus{
		Name:  "some-server",
		State: MCPServerReady,
		Server: MCPServerInfo{
			Name:    "lookup-server",
			Title:   &title,
			Version: "1.0.0",
		},
		Tools: []MCPToolInfo{{Name: "look-up.raw", Description: "Look up test data."}},
	})
	service.SetServer(MCPServerStatus{
		Name:  "some_server",
		State: MCPServerReady,
		Tools: []MCPToolInfo{{Name: "underscore_lookup"}},
	})

	response, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal status returned error: %v", err)
	}
	var payload struct {
		Data []struct {
			Name       string                 `json:"name"`
			ServerInfo *MCPServerInfo         `json:"serverInfo"`
			Tools      map[string]MCPToolInfo `json:"tools"`
			Resources  []MCPResource          `json:"resources"`
			Templates  []MCPResourceTemplate  `json:"resourceTemplates"`
			AuthStatus MCPAuthStatus          `json:"authStatus"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal status returned error: %v", err)
	}
	if payload.NextCursor != nil || len(payload.Data) != 2 {
		t.Fatalf("status payload = %#v", payload)
	}
	statusTools := map[string][]string{}
	for _, status := range payload.Data {
		for toolName, tool := range status.Tools {
			statusTools[status.Name] = append(statusTools[status.Name], toolName)
			if tool.Name != toolName {
				t.Fatalf("tool key/name mismatch for %q: key=%q tool=%#v", status.Name, toolName, tool)
			}
		}
		if status.Name == "some-server" {
			if status.ServerInfo == nil || status.ServerInfo.Title == nil || *status.ServerInfo.Title != "Lookup Server" {
				t.Fatalf("serverInfo for some-server = %#v", status.ServerInfo)
			}
		}
		if status.Resources == nil || status.Templates == nil || status.AuthStatus == "" {
			t.Fatalf("status should emit Rust v2 inventory/auth fields: %#v", status)
		}
	}
	if strings.Join(statusTools["some-server"], ",") != "look-up.raw" || strings.Join(statusTools["some_server"], ",") != "underscore_lookup" {
		t.Fatalf("status tools = %#v", statusTools)
	}
}

func TestMCPAuthStatusIsUnknownWhenCredentialDiscoveryFailsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, mcpOAuthFallbackFilename), []byte("not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{
		CodexHome: home,
		Servers: map[string]ServerRegistration{
			"remote": {Config: ServerConfig{URL: "https://mcp.example.test/mcp", Enabled: true}},
		},
	})
	statuses := service.ListStatus(&MCPListServerStatusParams{})
	if len(statuses.Servers) != 1 || statuses.Servers[0].AuthStatus != MCPAuthUnknown {
		t.Fatalf("auth statuses = %#v, want unknown", statuses.Servers)
	}
}

func TestListStatusCheckedObserverReportsStartupLifecycle(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"missing": {
			Name: "missing",
			Config: ServerConfig{
				Command: "codex-go-missing-mcp-observer-test",
				Enabled: true,
			},
		},
	}})
	defer service.Close()

	type update struct {
		name   string
		status MCPServerStartupState
		err    error
	}
	var updates []update
	response, err := service.ListStatusCheckedWithObserver(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull},
	}, func(name string, status MCPServerStartupState, startupErr error) {
		updates = append(updates, update{name: name, status: status, err: startupErr})
	})
	if err != nil || response == nil {
		t.Fatalf("ListStatusCheckedWithObserver response=%#v err=%v", response, err)
	}
	if len(updates) != 2 || updates[0].name != "missing" || updates[0].status != MCPServerStarting {
		t.Fatalf("updates = %#v", updates)
	}
	if updates[1].status != MCPServerFailed || updates[1].err == nil {
		t.Fatalf("terminal update = %#v, want failed with error", updates[1])
	}
}

type mcpStartupTestUpdate struct {
	name   string
	status MCPServerStartupState
}

func TestListStatusCheckedInitializesServersConcurrently(t *testing.T) {
	if os.Getenv("MCP_CONCURRENT_FAST_HELPER") == "1" {
		runMCPHelperServer()
		return
	}
	releaseFile := t.TempDir() + string(os.PathSeparator) + "release-slow"
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"slow": {
			Config: ServerConfig{
				Command:        executable,
				Args:           []string{"-test.run=TestListStatusCheckedInitializesServersConcurrently", "--"},
				Env:            map[string]string{"MCP_CONCURRENT_FAST_HELPER": "1", "MCP_CONCURRENT_SLOW_HELPER": "1", "MCP_CONCURRENT_RELEASE_FILE": releaseFile},
				Enabled:        true,
				StartupTimeout: 5 * time.Second,
			},
		},
		"fast": {
			Config: ServerConfig{
				Command: executable,
				Args:    []string{"-test.run=TestListStatusCheckedInitializesServersConcurrently", "--"},
				Env:     map[string]string{"MCP_CONCURRENT_FAST_HELPER": "1"},
				Enabled: true,
			},
		},
	}})
	defer service.Close()

	var updates []mcpStartupTestUpdate
	response, err := service.ListStatusCheckedWithObserver(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull},
	}, func(name string, status MCPServerStartupState, startupErr error) {
		updates = append(updates, mcpStartupTestUpdate{name: name, status: status})
		if name == "fast" && status == MCPServerReady {
			if err := os.WriteFile(releaseFile, []byte("ready"), 0o600); err != nil {
				t.Fatalf("release slow helper: %v", err)
			}
		}
	})
	if err != nil || response == nil {
		t.Fatalf("ListStatusCheckedWithObserver response=%#v err=%v", response, err)
	}
	if !mcpUpdateBefore(updates, mcpStartupTestUpdate{name: "fast", status: MCPServerReady}, mcpStartupTestUpdate{name: "slow", status: MCPServerFailed}) {
		t.Fatalf("updates = %#v, want fast ready before slow failed", updates)
	}
	states := map[string]MCPServerStartupState{}
	for _, status := range response.Data {
		states[status.effectiveName()] = status.State
	}
	if !reflect.DeepEqual(states, map[string]MCPServerStartupState{"fast": MCPServerReady, "slow": MCPServerFailed}) {
		t.Fatalf("states = %#v", states)
	}
}

func TestListStatusCheckedOptionalStartupUsesSharedGraceAndWarmsCache(t *testing.T) {
	if os.Getenv("MCP_CONCURRENT_FAST_HELPER") == "1" {
		runMCPHelperServer()
		return
	}
	releaseFile := t.TempDir() + string(os.PathSeparator) + "release-optional"
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"optional": {Config: ServerConfig{
			Command: executable,
			Args:    []string{"-test.run=TestListStatusCheckedOptionalStartupUsesSharedGraceAndWarmsCache", "--"},
			Env: map[string]string{
				"MCP_CONCURRENT_FAST_HELPER":   "1",
				"MCP_OPTIONAL_STARTUP_BARRIER": "1",
				"MCP_CONCURRENT_RELEASE_FILE":  releaseFile,
			},
			Enabled: true, StartupTimeout: 5 * time.Second,
		}},
	}})
	defer service.Close()

	started := time.Now()
	response, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("optional startup blocked for %v", elapsed)
	}
	if len(response.Data) != 1 || len(response.Data[0].Tools) != 0 {
		t.Fatalf("pending optional response = %#v", response.Data)
	}
	started = time.Now()
	second, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
		NonBlockingOptional:  true,
		OptionalStartupGrace: 50 * time.Millisecond,
	})
	if err != nil || second == nil {
		t.Fatalf("second ListStatusChecked() response=%#v error=%v", second, err)
	}
	if elapsed := time.Since(started); elapsed > 25*time.Millisecond {
		t.Fatalf("second capture restarted optional startup grace: %v", elapsed)
	}
	if err := os.WriteFile(releaseFile, []byte("ready"), 0o600); err != nil {
		t.Fatalf("release optional helper: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statuses := service.ConfiguredStatuses()
		if len(statuses) == 1 && statuses[0].State == MCPServerReady && len(statuses[0].Tools) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("optional MCP did not publish its warmed tool cache")
}

func TestListStatusCheckedExplicitRequiredServerWaitsPastGrace(t *testing.T) {
	if os.Getenv("MCP_CONCURRENT_FAST_HELPER") == "1" {
		runMCPHelperServer()
		return
	}
	releaseFile := t.TempDir() + string(os.PathSeparator) + "release-required"
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"required-by-turn": {Config: ServerConfig{
			Command: executable,
			Args:    []string{"-test.run=TestListStatusCheckedExplicitRequiredServerWaitsPastGrace", "--"},
			Env: map[string]string{
				"MCP_CONCURRENT_FAST_HELPER":   "1",
				"MCP_OPTIONAL_STARTUP_BARRIER": "1",
				"MCP_CONCURRENT_RELEASE_FILE":  releaseFile,
			},
			Enabled: true, StartupTimeout: 5 * time.Second,
		}},
	}})
	defer service.Close()

	done := make(chan *MCPListServerStatusResponse, 1)
	go func() {
		response, _ := service.ListStatusChecked(&MCPListServerStatusParams{
			Detail:               &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
			RequiredServers:      []string{"required-by-turn"},
			NonBlockingOptional:  true,
			OptionalStartupGrace: 25 * time.Millisecond,
		})
		done <- response
	}()
	select {
	case <-done:
		t.Fatal("explicitly required MCP returned before startup completed")
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
	case <-time.After(3 * time.Second):
		t.Fatal("explicitly required MCP did not finish after release")
	}
}

func mcpUpdateBefore(updates []mcpStartupTestUpdate, before mcpStartupTestUpdate, after mcpStartupTestUpdate) bool {
	beforeIndex := -1
	afterIndex := -1
	for i, update := range updates {
		if update == before && beforeIndex < 0 {
			beforeIndex = i
		}
		if update == after && afterIndex < 0 {
			afterIndex = i
		}
	}
	return beforeIndex >= 0 && afterIndex >= 0 && beforeIndex < afterIndex
}

func TestMCPServerStatusResourceWireShapeMatchesRustV2(t *testing.T) {
	size := int64(42)
	encoded, err := json.Marshal(&MCPServerStatus{
		Name: "docs",
		Resources: []MCPResource{{
			Server:      "internal-server",
			URI:         "file://demo",
			Name:        "demo",
			Title:       "Demo",
			Description: "Demo resource",
			MimeType:    "text/plain",
			Size:        &size,
			Annotations: map[string]any{"audience": "assistant"},
			Icons:       []any{map[string]any{"src": "file.png"}},
			Meta:        map[string]any{"trace": "resource"},
		}},
		ResourceTemplates: []MCPResourceTemplate{{
			Server:      "internal-server",
			URITemplate: "file://{path}",
			Name:        "file",
			Title:       "File",
			Description: "File template",
			MimeType:    "text/plain",
			Annotations: map[string]any{"audience": "assistant"},
			Meta:        map[string]any{"trace": "template"},
		}},
	})
	if err != nil {
		t.Fatalf("Marshal status returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal status returned error: %v", err)
	}
	resources := payload["resources"].([]any)
	resource := resources[0].(map[string]any)
	if resource["uri"] != "file://demo" || resource["name"] != "demo" || resource["size"] != float64(42) {
		t.Fatalf("resource payload = %#v", resource)
	}
	if _, ok := resource["server"]; ok {
		t.Fatalf("status resource should not emit internal server key: %#v", resource)
	}
	if _, ok := resource["icons"].([]any); !ok {
		t.Fatalf("status resource should keep Rust Resource icons: %#v", resource)
	}
	if meta, ok := resource["_meta"].(map[string]any); !ok || meta["trace"] != "resource" {
		t.Fatalf("status resource should keep Rust Resource _meta: %#v", resource)
	}

	templates := payload["resourceTemplates"].([]any)
	template := templates[0].(map[string]any)
	if template["uriTemplate"] != "file://{path}" || template["name"] != "file" {
		t.Fatalf("template payload = %#v", template)
	}
	for _, omitted := range []string{"server", "icons", "_meta"} {
		if _, ok := template[omitted]; ok {
			t.Fatalf("status resource template should not emit %q: %#v", omitted, template)
		}
	}
}

func TestMCPServerStatusWireIncludesStateAndError(t *testing.T) {
	message := "startup failed"
	encoded, err := json.Marshal(&MCPServerStatus{
		Name:  "docs",
		State: MCPServerFailed,
		Error: &message,
	})
	if err != nil {
		t.Fatalf("Marshal status returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("Unmarshal status returned error: %v", err)
	}
	if payload["state"] != string(MCPServerFailed) || payload["error"] != message {
		t.Fatalf("failed status payload = %#v", payload)
	}

	encodedDefault, err := json.Marshal(&MCPServerStatus{Name: "default"})
	if err != nil {
		t.Fatalf("Marshal default status returned error: %v", err)
	}
	var defaultPayload map[string]any
	if err := json.Unmarshal(encodedDefault, &defaultPayload); err != nil {
		t.Fatalf("Unmarshal default status returned error: %v", err)
	}
	if defaultPayload["state"] != string(MCPServerReady) {
		t.Fatalf("default status payload = %#v", defaultPayload)
	}
}

func TestMCPServerStatusDetailZeroValueMatchesToolsAndAuthOnly(t *testing.T) {
	service := NewMCPService(nil)
	service.SetServer(MCPServerStatus{
		Name:      "docs",
		State:     MCPServerReady,
		Tools:     []MCPToolInfo{{Name: "read"}},
		Resources: []MCPResource{{URI: "file://demo"}},
	})

	direct, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{}})
	if err != nil {
		t.Fatalf("ListStatusChecked(zero detail) error = %v", err)
	}
	if len(direct.Data) != 1 || len(direct.Data[0].Tools) != 1 || len(direct.Data[0].Resources) != 0 {
		t.Fatalf("zero detail status = %#v", direct.Data)
	}

	var stringParams MCPListServerStatusParams
	if err := json.Unmarshal([]byte(`{"detail":"toolsAndAuthOnly"}`), &stringParams); err != nil {
		t.Fatalf("Unmarshal string detail error = %v", err)
	}
	fromString, err := service.ListStatusChecked(&stringParams)
	if err != nil {
		t.Fatalf("ListStatusChecked(string detail) error = %v", err)
	}
	if len(fromString.Data) != 1 || len(fromString.Data[0].Tools) != 1 || len(fromString.Data[0].Resources) != 0 {
		t.Fatalf("string detail status = %#v", fromString.Data)
	}

	var legacyParams MCPListServerStatusParams
	if err := json.Unmarshal([]byte(`{"detail":{"includeTools":false}}`), &legacyParams); err != nil {
		t.Fatalf("Unmarshal legacy detail error = %v", err)
	}
	legacy, err := service.ListStatusChecked(&legacyParams)
	if err != nil {
		t.Fatalf("ListStatusChecked(legacy detail) error = %v", err)
	}
	if len(legacy.Data) != 1 || len(legacy.Data[0].Tools) != 0 || len(legacy.Data[0].Resources) != 0 {
		t.Fatalf("legacy detail status = %#v", legacy.Data)
	}
}

func TestApplyRuntimeConfigReplacesConfiguredServers(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"old": {Config: ServerConfig{Command: "old-mcp", Enabled: true, Required: true}},
	}})
	service.ApplyRuntimeConfig(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"new": {Config: ServerConfig{Command: "new-mcp", Enabled: true}},
	}})
	response := service.ListStatus(&MCPListServerStatusParams{})
	if len(response.Data) != 1 || response.Data[0].Name != "new" {
		t.Fatalf("status = %#v", response)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "old", ToolName: "read"}); err != nil {
		t.Fatalf("old optional fallback should remain callable after required config removed: %v", err)
	}
}

func TestApplyRuntimeConfigReusesHealthyConnectionForViewOnlyChanges(t *testing.T) {
	initial := ServerConfig{
		URL:          "https://example.test/mcp",
		Enabled:      true,
		EnabledTools: []string{"read"},
		ToolTimeout:  time.Second,
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: initial},
	}})
	initialRuntime, _ := service.serverConfig("docs")
	client := service.httpClientForServer("docs", &initialRuntime)
	service.SetServer(MCPServerStatus{
		Name:  "docs",
		State: MCPServerReady,
		Tools: []MCPToolInfo{{Name: "read"}},
	})

	updated := cloneServerConfig(&initial)
	updated.EnabledTools = []string{"write"}
	updated.ToolTimeout = 2 * time.Second
	service.ApplyRuntimeConfig(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: updated},
	}})

	updatedRuntime, _ := service.serverConfig("docs")
	if got := service.httpClientForServer("docs", &updatedRuntime); got != client {
		t.Fatalf("ApplyRuntimeConfig() replaced a healthy connection for view-only changes: old=%s new=%s", mcpConnectionCacheKey(&initialRuntime, false), mcpConnectionCacheKey(&updatedRuntime, false))
	}
	status := service.ConfiguredStatuses()
	if len(status) != 1 || len(status[0].Tools) != 1 || status[0].Tools[0].Name != "read" {
		t.Fatalf("ApplyRuntimeConfig() did not preserve published inventory: %#v", status)
	}
}

func TestApplyRuntimeConfigReplacesChangedOrClosedConnections(t *testing.T) {
	initial := ServerConfig{URL: "https://example.test/one", Enabled: true}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: initial},
	}})
	initialRuntime, _ := service.serverConfig("docs")
	first := service.httpClientForServer("docs", &initialRuntime)

	changed := cloneServerConfig(&initial)
	changed.URL = "https://example.test/two"
	service.ApplyRuntimeConfig(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: changed},
	}})
	changedRuntime, _ := service.serverConfig("docs")
	second := service.httpClientForServer("docs", &changedRuntime)
	if second == first {
		t.Fatal("ApplyRuntimeConfig() reused a connection after its identity changed")
	}
	if !first.isClosed() {
		t.Fatal("ApplyRuntimeConfig() did not close the replaced connection")
	}

	if err := second.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	service.ApplyRuntimeConfig(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: changed},
	}})
	changedRuntime, _ = service.serverConfig("docs")
	if got := service.httpClientForServer("docs", &changedRuntime); got == second {
		t.Fatal("ApplyRuntimeConfig() reused a closed connection")
	}
}

func TestApplyRuntimeConfigPreservesRefreshedAppsTools(t *testing.T) {
	config := ServerConfig{URL: "https://example.test/apps", Enabled: true}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {Config: config},
	}})
	appsRuntime, _ := service.serverConfig(CodexAppsServerName)
	client := service.httpClientForServer(CodexAppsServerName, &appsRuntime)
	service.SetServer(MCPServerStatus{
		Name:  CodexAppsServerName,
		State: MCPServerReady,
		Tools: []MCPToolInfo{{Name: "newly_installed"}},
	})

	service.ApplyRuntimeConfig(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {Config: config},
		"other":             {Config: ServerConfig{Command: "other", Enabled: true}},
	}})

	appsRuntime, _ = service.serverConfig(CodexAppsServerName)
	if got := service.httpClientForServer(CodexAppsServerName, &appsRuntime); got != client {
		t.Fatal("ApplyRuntimeConfig() replaced the Apps connection during unrelated refresh")
	}
	status := service.ConfiguredStatuses()
	for _, server := range status {
		if server.effectiveName() == CodexAppsServerName {
			if len(server.Tools) != 1 || server.Tools[0].Name != "newly_installed" {
				t.Fatalf("Apps tools after runtime refresh = %#v", server.Tools)
			}
			return
		}
	}
	t.Fatal("Apps server missing after runtime refresh")
}

func TestMCPOAuthAndContentJSONShape(t *testing.T) {
	login := &MCPServerOauthLoginResponse{AuthorizationURL: "https://example.test/oauth", URL: "legacy"}
	encodedLogin, err := json.Marshal(login)
	if err != nil {
		t.Fatalf("Marshal login returned error: %v", err)
	}
	var loginPayload map[string]any
	if err := json.Unmarshal(encodedLogin, &loginPayload); err != nil {
		t.Fatalf("Unmarshal login returned error: %v", err)
	}
	if loginPayload["authorizationUrl"] != "https://example.test/oauth" {
		t.Fatalf("login payload = %#v", loginPayload)
	}
	if _, ok := loginPayload["url"]; ok {
		t.Fatalf("legacy url key should not be emitted: %#v", loginPayload)
	}

	var content MCPToolCallContent
	if err := json.Unmarshal([]byte(`{"type":"image","data":"abc","mimeType":"image/png"}`), &content); err != nil {
		t.Fatalf("content unmarshal returned error: %v", err)
	}
	if content.Type != "image" || content.Map()["mimeType"] != "image/png" {
		t.Fatalf("content = %#v map=%#v", content, content.Map())
	}
	encodedContent, err := json.Marshal(&MCPToolCallResponse{Content: []MCPToolCallContent{content}})
	if err != nil {
		t.Fatalf("Marshal content returned error: %v", err)
	}
	if !strings.Contains(string(encodedContent), `"mimeType":"image/png"`) {
		t.Fatalf("encoded content = %s", encodedContent)
	}
	emptyText, err := json.Marshal(&MCPResourceReadResponse{Contents: []MCPResourceContent{{URI: "file://empty"}}})
	if err != nil {
		t.Fatalf("Marshal empty resource returned error: %v", err)
	}
	if !strings.Contains(string(emptyText), `"text":""`) {
		t.Fatalf("empty resource text should be emitted: %s", emptyText)
	}

	falseValue := false
	falseError, err := json.Marshal(&MCPToolCallResponse{Content: []MCPToolCallContent{}, IsError: &falseValue})
	if err != nil {
		t.Fatalf("Marshal false isError returned error: %v", err)
	}
	if !strings.Contains(string(falseError), `"isError":false`) {
		t.Fatalf("explicit false isError should be emitted: %s", falseError)
	}
	omittedError, err := json.Marshal(&MCPToolCallResponse{Content: []MCPToolCallContent{}})
	if err != nil {
		t.Fatalf("Marshal omitted isError returned error: %v", err)
	}
	if strings.Contains(string(omittedError), `"isError"`) {
		t.Fatalf("nil isError should be omitted: %s", omittedError)
	}
}

func TestMCPParamsMarshalRustV2Shape(t *testing.T) {
	threadID := "thread-live"
	timeoutSecs := uint64(30)
	encodedLogin, err := json.Marshal(MCPServerOauthLoginParams{
		Name:        "canonical",
		ServerName:  "legacy",
		ThreadID:    &threadID,
		Scopes:      []string{"read"},
		TimeoutSecs: &timeoutSecs,
	})
	if err != nil {
		t.Fatalf("Marshal oauth params returned error: %v", err)
	}
	var loginPayload map[string]any
	if err := json.Unmarshal(encodedLogin, &loginPayload); err != nil {
		t.Fatalf("Unmarshal oauth params returned error: %v", err)
	}
	if loginPayload["name"] != "canonical" || loginPayload["threadId"] != "thread-live" || loginPayload["timeoutSecs"] != float64(30) {
		t.Fatalf("oauth params payload = %#v", loginPayload)
	}
	if _, ok := loginPayload["serverName"]; ok {
		t.Fatalf("legacy serverName should not be emitted: %#v", loginPayload)
	}
	dcr := MCPServerOauthClientRegistrationDcr
	encodedLoginWithRegistration, err := json.Marshal(MCPServerOauthLoginParams{
		Name:               "canonical",
		ClientRegistration: &dcr,
	})
	if err != nil {
		t.Fatalf("Marshal oauth params with registration returned error: %v", err)
	}
	if !strings.Contains(string(encodedLoginWithRegistration), `"clientRegistration":"dcr"`) {
		t.Fatalf("oauth params with clientRegistration = %s", encodedLoginWithRegistration)
	}

	encodedResource, err := json.Marshal(MCPResourceReadParams{
		ThreadID:   &threadID,
		ServerName: "legacy-resource",
		URI:        "file://demo",
	})
	if err != nil {
		t.Fatalf("Marshal resource params returned error: %v", err)
	}
	var resourcePayload map[string]any
	if err := json.Unmarshal(encodedResource, &resourcePayload); err != nil {
		t.Fatalf("Unmarshal resource params returned error: %v", err)
	}
	if resourcePayload["server"] != "legacy-resource" || resourcePayload["uri"] != "file://demo" {
		t.Fatalf("resource params payload = %#v", resourcePayload)
	}
	if _, ok := resourcePayload["serverName"]; ok {
		t.Fatalf("legacy serverName should not be emitted: %#v", resourcePayload)
	}

	encodedTool, err := json.Marshal(MCPToolCallParams{
		ThreadID:   "thread-live",
		ServerName: "legacy-tool-server",
		ToolName:   "legacy-tool",
		Arguments:  map[string]any{"path": "README.md"},
		Meta:       map[string]any{"source": "test-client"},
	})
	if err != nil {
		t.Fatalf("Marshal tool params returned error: %v", err)
	}
	var toolPayload map[string]any
	if err := json.Unmarshal(encodedTool, &toolPayload); err != nil {
		t.Fatalf("Unmarshal tool params returned error: %v", err)
	}
	if toolPayload["threadId"] != "thread-live" || toolPayload["server"] != "legacy-tool-server" || toolPayload["tool"] != "legacy-tool" {
		t.Fatalf("tool params payload = %#v", toolPayload)
	}
	if _, ok := toolPayload["serverName"]; ok {
		t.Fatalf("legacy serverName should not be emitted: %#v", toolPayload)
	}
	if _, ok := toolPayload["toolName"]; ok {
		t.Fatalf("legacy toolName should not be emitted: %#v", toolPayload)
	}
}

func TestMCPServerOauthClientRegistrationValidation(t *testing.T) {
	bogus := MCPServerOauthClientRegistration("bogus")
	if _, err := mcpServerOauthClientRegistration(&MCPServerOauthLoginParams{ClientRegistration: &bogus}); err == nil || !strings.Contains(err.Error(), "clientRegistration must be one of: auto, cimd, dcr") {
		t.Fatalf("bogus clientRegistration error = %v", err)
	}
	cimd := MCPServerOauthClientRegistrationCimd
	value, err := mcpServerOauthClientRegistration(&MCPServerOauthLoginParams{ClientRegistration: &cimd})
	if err != nil || value != MCPServerOauthClientRegistrationCimd {
		t.Fatalf("cimd clientRegistration = %q/%v, want cimd (Rust #38089)", value, err)
	}
	dcr := MCPServerOauthClientRegistration("Dcr")
	value, err = mcpServerOauthClientRegistration(&MCPServerOauthLoginParams{ClientRegistration: &dcr})
	if err != nil || value != MCPServerOauthClientRegistrationDcr {
		t.Fatalf("Dcr clientRegistration = %q/%v, want dcr", value, err)
	}
	value, err = mcpServerOauthClientRegistration(&MCPServerOauthLoginParams{})
	if err != nil || value != MCPServerOauthClientRegistrationAuto {
		t.Fatalf("omitted clientRegistration = %q/%v, want auto", value, err)
	}
}

func TestMCPServiceOauthLoginForcedDCRRequiresRegistrationEndpoint(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{
		CodexHome: t.TempDir(),
		Servers: map[string]ServerRegistration{
			"no-registration": {Config: ServerConfig{URL: "http://127.0.0.1:1/mcp", Enabled: true}},
			"configured":      {Config: ServerConfig{URL: "http://127.0.0.1:1/mcp", Enabled: true, OAuthClientID: "configured-client"}},
		},
	})
	defer service.Close()
	dcr := MCPServerOauthClientRegistrationDcr
	_, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "no-registration", ClientRegistration: &dcr})
	if err == nil || !strings.Contains(err.Error(), "requires dynamic client registration") {
		t.Fatalf("forced DCR without registration endpoint error = %v", err)
	}
	// A configured OAuth client ID takes precedence over the registration
	// strategy (Rust 6dc3ac8721), so forced DCR still starts the login.
	if _, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "configured", ClientRegistration: &dcr}); err != nil {
		t.Fatalf("forced DCR with configured client id error = %v", err)
	}
}

func TestMCPToolCallMetaWithThreadID(t *testing.T) {
	merged := mcpToolCallMetaWithThreadID(map[string]any{"source": "client", "threadId": "stale"}, "thread-live")
	mergedMap, ok := merged.(map[string]any)
	if !ok || mergedMap["source"] != "client" || mergedMap["threadId"] != "thread-live" {
		t.Fatalf("merged meta = %#v", merged)
	}

	added := mcpToolCallMetaWithThreadID(nil, "thread-live")
	addedMap, ok := added.(map[string]any)
	if !ok || addedMap["threadId"] != "thread-live" {
		t.Fatalf("added meta = %#v", added)
	}

	passthrough := mcpToolCallMetaWithThreadID("invalid-meta", "thread-live")
	if passthrough != "invalid-meta" {
		t.Fatalf("passthrough meta = %#v", passthrough)
	}
}

func TestMCPToolCallMetaIncludesCallIDLikeRust(t *testing.T) {
	service := NewMCPService(nil)
	meta := service.augmentToolCallMeta(&MCPToolCallParams{
		ThreadID: "thread-call-id",
		Meta:     map[string]any{"source": "client"},
		CallID:   "call-123",
	})
	metaMap, ok := meta.(map[string]any)
	if !ok || metaMap["callId"] != "call-123" || metaMap["threadId"] != "thread-call-id" || metaMap["source"] != "client" {
		t.Fatalf("augmented meta = %#v", meta)
	}

	withExisting := service.augmentToolCallMeta(&MCPToolCallParams{
		Meta:   map[string]any{"callId": "client-provided"},
		CallID: "call-456",
	})
	existingMap, ok := withExisting.(map[string]any)
	if !ok || existingMap["callId"] != "call-456" {
		t.Fatalf("existing callId should be overridden: %#v", withExisting)
	}

	withoutCallID := service.augmentToolCallMeta(&MCPToolCallParams{Meta: map[string]any{"source": "client"}})
	if _, ok := withoutCallID.(map[string]any)["callId"]; ok {
		t.Fatalf("unexpected callId without CallID: %#v", withoutCallID)
	}
}

func TestMCPValidation(t *testing.T) {
	service := NewMCPService(nil)
	if _, err := service.OauthLogin(&MCPServerOauthLoginParams{}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "name is required" {
		t.Fatalf("OauthLogin() error = %v, want invalid MCP request", err)
	}
	if _, err := service.OauthLogin(nil); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "name is required" {
		t.Fatalf("OauthLogin(nil) error = %v, want invalid MCP request", err)
	}
	if _, err := service.ReadResource(&MCPResourceReadParams{ServerName: "s"}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "server and uri are required" {
		t.Fatalf("ReadResource() error = %v, want invalid MCP request", err)
	}
	if _, err := service.ReadResource(nil); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "server and uri are required" {
		t.Fatalf("ReadResource(nil) error = %v, want invalid MCP request", err)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "s"}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "server and tool are required" {
		t.Fatalf("CallTool() error = %v, want invalid MCP request", err)
	}
	if _, err := service.CallTool(nil); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "server and tool are required" {
		t.Fatalf("CallTool(nil) error = %v, want invalid MCP request", err)
	}
}

func TestMCPStatusPaginationInvalidRequestErrors(t *testing.T) {
	service := NewMCPService(nil)
	service.SetServer(MCPServerStatus{Server: MCPServerInfo{Name: "custom"}, State: MCPServerReady})

	invalidCursor := "bad"
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{Cursor: &invalidCursor}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "invalid cursor: bad" {
		t.Fatalf("ListStatusChecked(invalid cursor) error = %v", err)
	}

	blankCursor := "  "
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{Cursor: &blankCursor}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "invalid cursor:   " {
		t.Fatalf("ListStatusChecked(blank cursor) error = %v", err)
	}

	beyondCursor := "2"
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{Cursor: &beyondCursor}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != "cursor 2 exceeds total MCP servers 1" {
		t.Fatalf("ListStatusChecked(beyond cursor) error = %v", err)
	}
}

func TestMCPServiceOmitsDisabledRuntimeServersFromStatus(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"enabled":           {Config: ServerConfig{Command: "ok", Enabled: true}},
		"disabled":          {Config: ServerConfig{Command: "off", Enabled: false}},
		"required-disabled": {Config: ServerConfig{Command: "need", Enabled: false, Required: true}},
	}})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].effectiveName() != "enabled" {
		t.Fatalf("status = %#v, want only enabled server", status.Data)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "required-disabled", ToolName: "read"}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != `required MCP server "required-disabled" is not enabled` {
		t.Fatalf("CallTool(required disabled) error = %v", err)
	}
}

func TestNewMCPServiceNormalizesRuntimeServerNames(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		" docs ": {Config: ServerConfig{Enabled: true, Required: true}},
	}})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].effectiveName() != "docs" {
		t.Fatalf("status = %#v, want canonical docs", status.Data)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "read"}); err != nil {
		t.Fatalf("CallTool(canonical docs) error = %v", err)
	}
}

func TestMCPServiceSetServerConfigDisabledClearsStatus(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{Command: "docs", Enabled: true, Required: true}},
	}})
	service.SetServerConfig("docs", &ServerConfig{Command: "docs", Enabled: false, Required: true})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 0 {
		t.Fatalf("status = %#v, want disabled server omitted", status.Data)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "docs", ToolName: "read"}); !errors.Is(err, ErrInvalidMCPRequest) || err.Error() != `required MCP server "docs" is not enabled` {
		t.Fatalf("CallTool(required disabled) error = %v", err)
	}
}

func TestMCPServiceSetServerConfigEnabledCreatesStatus(t *testing.T) {
	service := NewMCPService(nil)
	service.SetServerConfig("docs", &ServerConfig{Command: "docs-mcp", Args: []string{"--stdio"}, Enabled: true})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].effectiveName() != "docs" || status.Data[0].State != MCPServerReady {
		t.Fatalf("status = %#v, want enabled docs ready", status.Data)
	}
	if status.Data[0].Server.Command != "docs-mcp" || strings.Join(status.Data[0].Server.Args, ",") != "--stdio" {
		t.Fatalf("server info = %#v", status.Data[0].Server)
	}
}

func TestRequiredMCPServerUnavailableErrors(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"required": {Config: ServerConfig{Command: "missing-helper", Enabled: true, Required: true}},
	}})
	message := "startup failed"
	service.SetServer(MCPServerStatus{Server: MCPServerInfo{Name: "required"}, State: MCPServerFailed, Error: &message})

	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "required", ToolName: "read"}); !errors.Is(err, ErrInvalidMCPRequest) || !strings.Contains(err.Error(), "required MCP server") {
		t.Fatalf("CallTool error = %v", err)
	}
	if _, err := service.ReadResource(&MCPResourceReadParams{ServerName: "required", URI: "file://x"}); !errors.Is(err, ErrInvalidMCPRequest) || !strings.Contains(err.Error(), "startup failed") {
		t.Fatalf("ReadResource error = %v", err)
	}
}

func TestValidateRequiredServersAggregatesFailuresAndIgnoresOptional(t *testing.T) {
	helperCommand, helperArgs := helperMCPServerCommand(t)
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"required-ready": {
			Config: ServerConfig{Command: helperCommand, Args: helperArgs, Env: map[string]string{"GO_WANT_MCP_HELPER": "1"}, Enabled: true, Required: true},
		},
		"required-zeta": {
			Config: ServerConfig{Command: "codex-required-zeta-does-not-exist", Enabled: true, Required: true},
		},
		"required-alpha": {
			Config: ServerConfig{Command: "codex-required-alpha-does-not-exist", Enabled: true, Required: true},
		},
		"optional-broken": {
			Config: ServerConfig{Command: "codex-optional-does-not-exist", Enabled: true},
		},
	}})
	t.Cleanup(func() { _ = service.Close() })

	err := service.ValidateRequiredServers("thread-required")
	if err == nil {
		t.Fatal("ValidateRequiredServers() error = nil")
	}
	message := err.Error()
	if !strings.HasPrefix(message, "required MCP servers failed to initialize: ") {
		t.Fatalf("error = %q", message)
	}
	alpha := strings.Index(message, "required-alpha:")
	zeta := strings.Index(message, "required-zeta:")
	if alpha < 0 || zeta < 0 || alpha >= zeta {
		t.Fatalf("required failures are not sorted: %q", message)
	}
	if strings.Contains(message, "optional-broken") || strings.Contains(message, "required-ready") {
		t.Fatalf("error includes healthy/optional servers: %q", message)
	}
}

func TestValidateRequiredServersAllowsOptionalFailure(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"optional-broken": {
			Config: ServerConfig{Command: "codex-optional-does-not-exist", Enabled: true},
		},
	}})
	t.Cleanup(func() { _ = service.Close() })
	if err := service.ValidateRequiredServers("thread-optional"); err != nil {
		t.Fatalf("ValidateRequiredServers(optional) error = %v", err)
	}
}

func TestValidateRequiredServersSkipsOptionalInventoryWhenNoneAreRequired(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"optional": {
			Config: ServerConfig{Command: "codex-optional-does-not-exist", Enabled: true},
		},
	}})
	t.Cleanup(func() { _ = service.Close() })
	before := service.Generation()
	if err := service.ValidateRequiredServers("thread-optional"); err != nil {
		t.Fatalf("ValidateRequiredServers(optional) error = %v", err)
	}
	if after := service.Generation(); after != before {
		t.Fatalf("optional-only validation changed generation: before=%d after=%d", before, after)
	}
	status := service.ConfiguredStatuses()
	if len(status) != 1 || len(status[0].Tools) != 0 || status[0].Error != nil {
		t.Fatalf("optional-only validation performed inventory: %#v", status)
	}
}

func TestOptionalMCPServerMissingKeepsFallback(t *testing.T) {
	service := NewMCPService(nil)
	call, err := service.CallTool(&MCPToolCallParams{ServerName: "optional", ToolName: "read", Arguments: map[string]any{"path": "a"}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(call.Content) != 1 || !strings.Contains(call.Content[0].Text, "path") {
		t.Fatalf("call = %#v", call)
	}
}

func TestStdioMCPToolListAndCall(t *testing.T) {
	command, args := helperMCPServerCommand(t)
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"stdio": {Config: ServerConfig{Command: command, Args: args, Env: map[string]string{"GO_WANT_MCP_HELPER": "1"}, Enabled: true}},
	}})
	response, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Tools) != 1 || response.Data[0].Tools[0].Name != "echo" {
		t.Fatalf("status = %#v", response)
	}
	call, err := service.CallTool(&MCPToolCallParams{
		ThreadID:   "thread-live",
		ServerName: "stdio",
		ToolName:   "echo",
		Arguments:  map[string]any{"text": "hi"},
		Meta:       map[string]any{"source": "test-client", "threadId": "stale-thread"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(call.Content) != 1 || !strings.Contains(call.Content[0].Text, `"text":"hi"`) || !strings.Contains(call.Content[0].Text, `"threadId":"thread-live"`) {
		t.Fatalf("call = %#v", call)
	}
	resource, err := service.ReadResource(&MCPResourceReadParams{ServerName: "stdio", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].Text != "resource:file://demo" {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	runMCPHelperServer()
	os.Exit(0)
}

func helperMCPServerCommand(t *testing.T) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	return exe, []string{"-test.run=TestMCPStdioHelperProcess"}
}

func runMCPHelperServer() {
	if os.Getenv("MCP_OPTIONAL_STARTUP_BARRIER") == "1" {
		releaseFile := os.Getenv("MCP_CONCURRENT_RELEASE_FILE")
		for {
			if _, err := os.Stat(releaseFile); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if os.Getenv("MCP_CONCURRENT_SLOW_HELPER") == "1" {
		releaseFile := os.Getenv("MCP_CONCURRENT_RELEASE_FILE")
		for {
			if _, err := os.Stat(releaseFile); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     *int64          `json:"id,omitempty"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return
		}
		if request.ID == nil {
			continue
		}
		switch request.Method {
		case "initialize":
			writeHelperMCPResponse(*request.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "tools/list":
			writeHelperMCPResponse(*request.ID, map[string]any{
				"tools": []map[string]any{{"name": "echo", "description": "Echo arguments"}},
			})
		case "tools/call":
			var params struct {
				Arguments any `json:"arguments"`
				Meta      any `json:"_meta"`
			}
			_ = json.Unmarshal(request.Params, &params)
			encoded, _ := json.Marshal(map[string]any{"arguments": params.Arguments, "_meta": params.Meta})
			writeHelperMCPResponse(*request.ID, map[string]any{
				"content": []map[string]string{{"type": "text", "text": string(encoded)}},
			})
		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(request.Params, &params)
			writeHelperMCPResponse(*request.ID, map[string]any{
				"contents": []map[string]string{{"uri": params.URI, "mimeType": "text/plain", "text": "resource:" + params.URI}},
			})
		case "resources/list":
			writeHelperMCPResponse(*request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeHelperMCPResponse(*request.ID, map[string]any{"resourceTemplates": []any{}})
		case "shutdown":
			writeHelperMCPResponse(*request.ID, map[string]any{})
			return
		default:
			writeHelperMCPError(*request.ID, -32601, "method not found")
		}
	}
}

func writeHelperMCPResponse(id int64, result any) {
	_ = writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeHelperMCPError(id int64, code int64, message string) {
	_ = writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_MCP_HELPER") == "1" {
		runMCPHelperServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestMCPServerStatusSerializesPluginIDLikeRust(t *testing.T) {
	pluginID := "acme/weather"
	status := MCPServerStatus{
		Name:     "docs",
		PluginID: &pluginID,
		Server:   MCPServerInfo{Name: "docs"},
		State:    MCPServerReady,
	}
	encoded, err := json.Marshal(&status)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["pluginId"] != "acme/weather" {
		t.Fatalf("pluginId = %#v", payload["pluginId"])
	}
	noPlugin := MCPServerStatus{Name: "config", Server: MCPServerInfo{Name: "config"}, State: MCPServerReady}
	encodedNo, err := json.Marshal(&noPlugin)
	if err != nil {
		t.Fatal(err)
	}
	var payloadNo map[string]any
	if err := json.Unmarshal(encodedNo, &payloadNo); err != nil {
		t.Fatal(err)
	}
	if value, ok := payloadNo["pluginId"]; !ok || value != nil {
		t.Fatalf("pluginId for non-plugin server = %#v (present=%v)", value, ok)
	}
}

func TestNewMCPServicePopulatesPluginIDFromRegistration(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{
		Servers: map[string]ServerRegistration{
			"docs": {
				Name:     "docs",
				Config:   ServerConfig{Enabled: true},
				Source:   "plugin",
				PluginID: "acme/weather",
			},
			"local": {
				Name:   "local",
				Config: ServerConfig{Enabled: true},
				Source: "config",
			},
		},
	})
	if status, ok := service.servers["docs"]; !ok || status.PluginID == nil || *status.PluginID != "acme/weather" {
		t.Fatalf("plugin server status = %#v", status)
	}
	if status, ok := service.servers["local"]; !ok || status.PluginID != nil {
		t.Fatalf("config server status = %#v", status)
	}
}

func TestHasExplicitHTTPAuthorizationMatchesRust(t *testing.T) {
	tests := []struct {
		name   string
		config *ServerConfig
		want   bool
	}{
		{name: "nil"},
		{name: "stdio", config: &ServerConfig{Command: "mcp-server", Enabled: true}},
		{name: "no headers", config: &ServerConfig{URL: "https://example.test/mcp", Enabled: true}},
		{name: "authorization header", config: &ServerConfig{
			URL:         "https://example.test/mcp",
			HTTPHeaders: map[string]string{"Authorization": "Bearer cached-http-test-token"},
		}, want: true},
		{name: "mixed-case authorization header", config: &ServerConfig{
			URL:         "https://example.test/mcp",
			HTTPHeaders: map[string]string{"aUtHoRiZaTiOn": "Bearer cached-http-test-token"},
		}, want: true},
		{name: "empty authorization", config: &ServerConfig{
			URL:         "https://example.test/mcp",
			HTTPHeaders: map[string]string{"Authorization": ""},
		}},
		{name: "whitespace authorization", config: &ServerConfig{
			URL:         "https://example.test/mcp",
			HTTPHeaders: map[string]string{"Authorization": " \t "},
		}},
		{name: "non-authorization header only", config: &ServerConfig{
			URL:         "https://example.test/mcp",
			HTTPHeaders: map[string]string{"X-API-Key": "secret"},
		}},
		{name: "bearer token env var", config: &ServerConfig{
			URL:               "https://example.test/mcp",
			HTTPHeaders:       map[string]string{"Authorization": "Bearer cached-http-test-token"},
			BearerTokenEnvVar: "MCP_TOKEN",
		}},
		{name: "env http headers", config: &ServerConfig{
			URL:            "https://example.test/mcp",
			HTTPHeaders:    map[string]string{"Authorization": "Bearer cached-http-test-token"},
			EnvHTTPHeaders: map[string]string{"X-Trace": "TRACE_TOKEN"},
		}},
		{name: "control characters rejected", config: &ServerConfig{
			URL:         "https://example.test/mcp",
			HTTPHeaders: map[string]string{"Authorization": "Bearer cached-http-test-token\r\nX: y"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasExplicitHTTPAuthorization(test.config); got != test.want {
				t.Fatalf("hasExplicitHTTPAuthorization() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMCPToolCatalogCacheEligibleMatchesRust(t *testing.T) {
	tests := []struct {
		name                     string
		config                   *ServerConfig
		oauthCredentialsResolved bool
		want                     bool
	}{
		{name: "nil", want: true},
		{name: "stdio", config: &ServerConfig{Command: "mcp-server", Enabled: true}, want: true},
		{name: "stdio with oauth fields still eligible", config: &ServerConfig{
			Command: "mcp-server", OAuthClientID: "client-1", Enabled: true,
		}, want: true},
		{name: "http explicit authorization", config: &ServerConfig{
			URL: "https://example.test/mcp", HTTPHeaders: map[string]string{"Authorization": "Bearer token"},
		}, want: true},
		{name: "http oauth client id excluded", config: &ServerConfig{
			URL: "https://example.test/mcp", OAuthClientID: "client-1",
		}},
		{name: "http oauth resource excluded", config: &ServerConfig{
			URL: "https://example.test/mcp", OAuthResource: "https://resource.example.test",
		}},
		{name: "http scopes configured excluded", config: &ServerConfig{
			URL: "https://example.test/mcp", ScopesConfigured: true,
		}},
		{name: "http chatgpt without explicit auth excluded", config: &ServerConfig{
			URL: "https://example.test/mcp", Auth: ServerAuthChatGPT,
		}},
		{name: "http chatgpt with explicit auth eligible", config: &ServerConfig{
			URL: "https://example.test/mcp", Auth: ServerAuthChatGPT,
			HTTPHeaders: map[string]string{"Authorization": "Bearer chatgpt-token"},
		}, want: true},
		{name: "http no explicit auth with resolved oauth credentials excluded", config: &ServerConfig{
			URL: "https://example.test/mcp",
		}, oauthCredentialsResolved: true},
		{name: "http explicit auth with resolved oauth credentials still eligible", config: &ServerConfig{
			URL: "https://example.test/mcp", HTTPHeaders: map[string]string{"Authorization": "Bearer token"},
		}, oauthCredentialsResolved: true, want: true},
		{name: "http bearer token env var no explicit auth resolved oauth excluded", config: &ServerConfig{
			URL: "https://example.test/mcp", BearerTokenEnvVar: "MCP_TOKEN",
		}, oauthCredentialsResolved: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mcpToolCatalogCacheEligible(test.config, test.oauthCredentialsResolved); got != test.want {
				t.Fatalf("mcpToolCatalogCacheEligible() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMCPToolCatalogGraceKeyFingerprintsHTTPEnvironmentAndPlugin(t *testing.T) {
	base := &ServerConfig{
		URL:               "https://example.test/mcp",
		HTTPHeaders:       map[string]string{"Authorization": "Bearer cached-http-test-token", "X-Extra": "1"},
		BearerTokenEnvVar: "MCP_BEARER_TOKEN",
		EnvHTTPHeaders:    map[string]string{"X-Trace": "MCP_TRACE_TOKEN"},
		EnvironmentID:     "local",
	}
	firstKey, firstEligible := mcpToolCatalogGraceKey("docs", base, false, false, false)
	if !firstEligible {
		t.Fatal("explicit-auth HTTP config should be cache eligible")
	}

	t.Setenv("MCP_BEARER_TOKEN", "token-a")
	t.Setenv("MCP_TRACE_TOKEN", "trace-a")
	keyA, eligibleA := mcpToolCatalogGraceKey("docs", base, false, false, false)
	if !eligibleA {
		t.Fatal("explicit-auth HTTP config should be cache eligible with env values")
	}
	if keyA == firstKey {
		t.Fatal("grace key should differ when referenced env var values change")
	}

	pluginKey, pluginEligible := mcpToolCatalogGraceKey("docs", base, false, true, false)
	if !pluginEligible {
		t.Fatal("explicit-auth HTTP config should be cache eligible for plugins")
	}
	if pluginKey == keyA {
		t.Fatal("grace key should differ when agent plugin status changes")
	}

	openAIKey, openAIEligible := mcpToolCatalogGraceKey("docs", base, true, false, false)
	if !openAIEligible {
		t.Fatal("explicit-auth HTTP config should be cache eligible in openAI form")
	}
	if openAIKey == keyA {
		t.Fatal("grace key should differ when client capabilities change")
	}

	if _, eligible := mcpToolCatalogGraceKey("docs", &ServerConfig{
		URL: "https://example.test/oauth", OAuthClientID: "client-1",
	}, false, false, false); eligible {
		t.Fatal("OAuth HTTP config should stay out of the shared catalog cache")
	}

	reordered := *base
	reordered.HTTPHeaders = map[string]string{"X-Extra": "1", "Authorization": "Bearer cached-http-test-token"}
	keyReordered, eligibleReordered := mcpToolCatalogGraceKey("docs", &reordered, false, false, false)
	if !eligibleReordered {
		t.Fatal("explicit-auth HTTP config should be cache eligible after header reorder")
	}
	if keyReordered != keyA {
		t.Fatal("grace key should be insensitive to header map order")
	}
}

func TestMCPToolCatalogGraceKeySharesOnlyForEligibleServers(t *testing.T) {
	eligible := &ServerConfig{
		URL: "https://example.test/mcp", HTTPHeaders: map[string]string{"Authorization": "Bearer token"},
	}
	key, eligibleOK := mcpToolCatalogGraceKey("docs", eligible, false, false, false)
	if !eligibleOK {
		t.Fatal("explicit-auth HTTP config should be cache eligible")
	}
	first := sharedOptionalStartupDeadlineForKey(key, time.Now().Add(time.Minute))
	second := sharedOptionalStartupDeadlineForKey(key, time.Now().Add(2*time.Minute))
	if !first.Equal(second) {
		t.Fatal("eligible HTTP config should share the process-wide optional startup grace")
	}
	clearSharedOptionalStartupDeadlineForKey(key)

	oauth := &ServerConfig{URL: "https://example.test/oauth", OAuthClientID: "client-1"}
	if key, eligible := mcpToolCatalogGraceKey("docs", oauth, false, false, false); eligible {
		t.Fatalf("OAuth HTTP config should not produce a shared grace key, got %q", key)
	}
}
