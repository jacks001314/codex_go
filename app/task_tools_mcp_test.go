package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/apps"
	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/mcp"
	codextui "codex_go/tui"
)

// taskToolsMCPPost sends one JSON-RPC envelope to the hosted task-tools MCP
// server and decodes the response body.
func taskToolsMCPPost(t *testing.T, config map[string]any, request map[string]any) map[string]any {
	t.Helper()
	url, _ := config["url"].(string)
	if url == "" {
		t.Fatalf("task-tools config has no url: %#v", config)
	}
	headers, _ := config["http_headers"].(map[string]any)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	httpRequest, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for name, value := range headers {
		text, _ := value.(string)
		httpRequest.Header.Set(name, text)
	}
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode response %q: %v", string(data), err)
	}
	return decoded
}

// TestTaskToolsMCPServerServesNamespaceLikeRust covers Rust
// DynamicToolMcpHandler: the hosted server lists the task-tools namespace with
// read-only annotations, echoes the negotiated protocol version, and fails an
// in-flight call while the TUI connection is suspended.
func TestTaskToolsMCPServerServesNamespaceLikeRust(t *testing.T) {
	host := &taskToolsMCPHost{}
	defer host.close()
	host.attach(&remoteAppServerTUIClient{}, appserver.ThreadStartParams{}, nil)
	config := host.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)

	if mode, _ := config["default_tools_approval_mode"].(string); mode != "approve" {
		t.Fatalf("default_tools_approval_mode = %q, want approve", mode)
	}
	tools, _ := config["tools"].(map[string]any)
	for _, name := range []string{"create_thread", "send_message_to_thread", "fork_thread"} {
		entry, _ := tools[name].(map[string]any)
		if approval, _ := entry["approval_mode"].(string); approval != "prompt" {
			t.Fatalf("%s approval_mode = %q, want prompt", name, approval)
		}
	}

	initialized := taskToolsMCPPost(t, config, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-03-26"},
	})
	result, _ := initialized["result"].(map[string]any)
	if version, _ := result["protocolVersion"].(string); version != "2025-03-26" {
		t.Fatalf("protocolVersion = %q, want the negotiated value", version)
	}

	listed := taskToolsMCPPost(t, config, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	listResult, _ := listed["result"].(map[string]any)
	rawTools, _ := listResult["tools"].([]any)
	names := map[string]map[string]any{}
	for _, entry := range rawTools {
		tool, _ := entry.(map[string]any)
		name, _ := tool["name"].(string)
		names[name] = tool
	}
	for _, spec := range DynamicToolSpecs() {
		if spec.Namespace == nil {
			continue
		}
		for _, function := range spec.Namespace.Tools {
			if _, ok := names[function.Name]; !ok {
				t.Fatalf("tools/list is missing %q", function.Name)
			}
		}
	}
	annotations, _ := names["list_threads"]["annotations"].(map[string]any)
	if readOnly, _ := annotations["readOnlyHint"].(bool); !readOnly {
		t.Fatalf("list_threads readOnlyHint = %#v, want true", annotations)
	}
	createAnnotations, _ := names["create_thread"]["annotations"].(map[string]any)
	if readOnly, _ := createAnnotations["readOnlyHint"].(bool); readOnly {
		t.Fatalf("create_thread readOnlyHint = %#v, want false", createAnnotations)
	}

	// A suspended connection refuses the call instead of switching connections
	// or replaying a mutation (Rust "TUI is reconnecting; tool was not sent").
	host.suspend()
	call := taskToolsMCPPost(t, config, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_threads", "arguments": map[string]any{}, "_meta": map[string]any{"threadId": "thread-1"}},
	})
	rpcError, _ := call["error"].(map[string]any)
	if message, _ := rpcError["message"].(string); message != taskToolsMCPReconnectingError {
		t.Fatalf("tools/call error = %#v, want the reconnecting error", call)
	}
}

// TestRemoteStartThreadUsesTaskToolsMCPTransportLikeRust mirrors Rust
// ThreadToolTransport::Mcp: with the hosted server the thread/start request
// drops the dynamic-tools namespace and carries the `mcp_servers.codex_tui`
// config override pointing at the local listener.
func TestRemoteStartThreadUsesTaskToolsMCPTransportLikeRust(t *testing.T) {
	// The capability marker is persisted under the codex home.
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverConn := net.Pipe()
	var mu sync.Mutex
	var startParams map[string]any
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			if request.Method == string(appserver.MethodThreadStart) {
				var params map[string]any
				_ = json.Unmarshal(request.Params, &params)
				mu.Lock()
				startParams = params
				mu.Unlock()
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result":  map[string]any{"thread": map[string]any{"id": "thread-mcp"}},
			})
		}
	}()

	state := codextui.NewState(nil)
	messages := make(chan bubbletea.Msg, 32)
	host := &taskToolsMCPHost{}
	defer host.close()
	client := &remoteAppServerTUIClient{
		endpoint:  appserverdaemon.NewUnixSocketEndpoint("/tmp/codex-task-tools.sock"),
		state:     state,
		messages:  messages,
		unixDial:  func(context.Context, string) (net.Conn, error) { return clientConn, nil },
		taskTools: host,
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	template, err := remoteThreadStartParams(&cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	host.attach(client, template, client.registerDynamicToolThread)

	threadID, err := client.startThread(ctx, &cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("startThread: %v", err)
	}
	if threadID != "thread-mcp" {
		t.Fatalf("threadID = %q", threadID)
	}
	mu.Lock()
	params := startParams
	mu.Unlock()
	if _, ok := params["dynamicTools"]; ok {
		t.Fatalf("thread/start still carries dynamicTools: %#v", params["dynamicTools"])
	}
	config, _ := params["config"].(map[string]any)
	server, _ := config["mcp_servers."+DynamicToolNamespace].(map[string]any)
	if server == nil {
		t.Fatalf("thread/start config is missing the codex_tui MCP server: %#v", params["config"])
	}
	expected := host.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)
	if url, _ := server["url"].(string); url == "" || url != expected["url"] {
		t.Fatalf("thread/start MCP url = %q, want %q", url, expected["url"])
	}
	capability, ok := drainTaskToolsAvailable(messages, "thread-mcp")
	if !ok || !capability.Available {
		t.Fatalf("capability = %#v (found=%v), want available", capability, ok)
	}
}

// TestTaskToolsMCPServerServesTheMCPRuntimeLikeRust drives the hosted server
// through Go's real MCP runtime: the thread's `mcp_servers.codex_tui` config
// override initializes the server over HTTP and lists the whole task-tools
// namespace, which is what makes the MCP transport work end to end.
func TestTaskToolsMCPServerServesTheMCPRuntimeLikeRust(t *testing.T) {
	host := &taskToolsMCPHost{}
	defer host.close()
	host.attach(&remoteAppServerTUIClient{}, appserver.ThreadStartParams{}, nil)
	raw, ok := host.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)
	if !ok {
		t.Fatalf("task-tools host has no config: %#v", host.configValues())
	}
	config := mcp.ServerConfigFromValues(raw)
	if !config.Enabled {
		t.Fatalf("thread config must enable the server by default: %#v", config)
	}
	service := mcp.NewMCPService(&mcp.RuntimeConfig{
		Servers: map[string]mcp.ServerRegistration{
			DynamicToolNamespace: {Name: DynamicToolNamespace, Config: *config},
		},
	})
	defer service.Close()

	response, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	var status *mcp.MCPServerStatus
	for i := range response.Data {
		if response.Data[i].Name == DynamicToolNamespace {
			status = &response.Data[i]
		}
	}
	if status == nil {
		t.Fatalf("server status missing from %#v", response.Data)
	}
	if status.State != mcp.MCPServerReady {
		t.Fatalf("server state = %q (%v), want ready", status.State, status.Error)
	}
	names := map[string]bool{}
	for _, listed := range status.Tools {
		names[listed.Name] = true
	}
	for _, spec := range DynamicToolSpecs() {
		if spec.Namespace == nil {
			continue
		}
		for _, function := range spec.Namespace.Tools {
			if !names[function.Name] {
				t.Fatalf("runtime tool listing is missing %q: %#v", function.Name, names)
			}
		}
	}

	// The approval policy travels with the same config: approve by default,
	// prompt for the mutating task tools.
	resolved, ok := service.ServerConfigForServer(DynamicToolNamespace)
	if !ok || resolved.DefaultToolsApprovalMode == nil || *resolved.DefaultToolsApprovalMode != apps.AppToolApprovalApprove {
		t.Fatalf("default_tools_approval_mode = %#v, want approve", resolved.DefaultToolsApprovalMode)
	}
	create, ok := resolved.Tools["create_thread"]
	if !ok || create.ApprovalMode == nil || *create.ApprovalMode != apps.AppToolApprovalPrompt {
		t.Fatalf("create_thread approval_mode = %#v, want prompt", create.ApprovalMode)
	}
}

// TestTaskToolsMCPToolCallRoutesThroughTheAppServerLikeRust covers the full MCP
// transport for one tool call: an MCP `tools/call` on the hosted server runs the
// task tool against the TUI's app-server connection and returns its payload as
// text content.
func TestTaskToolsMCPToolCallRoutesThroughTheAppServerLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close()
		decoder := json.NewDecoder(serverConn)
		encoder := json.NewEncoder(serverConn)
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			result := map[string]any{}
			switch request.Method {
			case string(appserver.MethodThreadStart):
				result = map[string]any{"thread": map[string]any{"id": "thread-call"}}
			case string(appserver.MethodThreadList):
				result = map[string]any{"data": []any{map[string]any{"id": "thread-listed", "cwd": "/repo"}}}
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()

	state := codextui.NewState(nil)
	messages := make(chan bubbletea.Msg, 32)
	host := &taskToolsMCPHost{}
	defer host.close()
	client := &remoteAppServerTUIClient{
		endpoint:  appserverdaemon.NewUnixSocketEndpoint("/tmp/codex-task-tools-call.sock"),
		state:     state,
		messages:  messages,
		unixDial:  func(context.Context, string) (net.Conn, error) { return clientConn, nil },
		taskTools: host,
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	template, err := remoteThreadStartParams(&cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	host.attach(client, template, client.registerDynamicToolThread)
	if _, err := client.startThread(ctx, &cli.RootOptions{}, state); err != nil {
		t.Fatalf("startThread: %v", err)
	}

	config, ok := host.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)
	if !ok {
		t.Fatalf("task-tools host has no config: %#v", host.configValues())
	}
	response := taskToolsMCPPost(t, config, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_threads",
			"arguments": map[string]any{},
			"_meta":     map[string]any{"threadId": "thread-call"},
		},
	})
	listed, _ := response["result"].(map[string]any)
	if isError, _ := listed["isError"].(bool); isError {
		t.Fatalf("tools/call reported an error: %#v", response)
	}
	content, _ := listed["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call returned no content: %#v", response)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "thread-listed") {
		t.Fatalf("tools/call text = %q, want the app-server thread payload", text)
	}
}
