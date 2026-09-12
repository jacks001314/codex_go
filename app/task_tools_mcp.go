package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"

	"codex_go/appserver"
	"codex_go/doctor"
)

// Rust parity: codex-rs/tui/src/dynamic_tools_mcp.rs. A TUI connected to a local
// daemon hosts its `codex_tui` task-management tools as a streamable-HTTP MCP
// server and points the thread's `mcp_servers.codex_tui` config at it, so the
// daemon's app server calls the tools like any other MCP server (with
// approval-mode gating) instead of routing `item/tool/call` back to the TUI.

const (
	taskToolsMCPPath              = "/mcp"
	taskToolsMCPServerName        = DynamicToolNamespace
	taskToolsMCPProtocolVersion   = "2025-06-18"
	taskToolsMCPMaxRequestBytes   = 4 << 20
	taskToolsMCPReconnectingError = "TUI is reconnecting; tool was not sent"
)

// taskToolsMCPReadOnlyTools mirrors Rust's list_tools annotations.
var taskToolsMCPReadOnlyTools = map[string]bool{
	"list_threads":          true,
	"list_archived_threads": true,
	"read_thread":           true,
	"wait_threads":          true,
}

// taskToolsMCPConnection is the live TUI connection a hosted MCP server uses to
// execute tool calls (Rust DynamicToolMcpServer.connection).
type taskToolsMCPConnection struct {
	client   *remoteAppServerTUIClient
	template appserver.ThreadStartParams
	register func(threadID string, taskToolsAvailable bool) error
}

// taskToolsMCPServer ports Rust DynamicToolMcpServer.
type taskToolsMCPServer struct {
	listener net.Listener
	server   *http.Server
	token    string
	config   map[string]any

	mu         sync.Mutex
	connection *taskToolsMCPConnection
}

// startTaskToolsMCPServer binds a loopback listener, serves the MCP endpoints and
// returns the server plus the `mcp_servers.codex_tui` config the TUI injects into
// thread start/fork params (Rust DynamicToolMcpServer::start).
func startTaskToolsMCPServer(connection *taskToolsMCPConnection) (*taskToolsMCPServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	server := &taskToolsMCPServer{
		listener:   listener,
		token:      "Bearer " + uuid.NewString(),
		connection: connection,
	}
	server.config = map[string]any{
		"url":          "http://" + listener.Addr().String() + taskToolsMCPPath,
		"http_headers": map[string]any{"Authorization": server.token},
		// Rust dynamic_tools_mcp server_config: task mutation tools prompt for
		// approval, everything else is approved.
		"default_tools_approval_mode": "approve",
		"tools": map[string]any{
			"create_thread":          map[string]any{"approval_mode": "prompt"},
			"send_message_to_thread": map[string]any{"approval_mode": "prompt"},
			"fork_thread":            map[string]any{"approval_mode": "prompt"},
		},
	}
	server.server = &http.Server{Handler: &taskToolsMCPHandler{server: server}}
	go func() {
		if err := server.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The TUI surfaces the failure through tool calls; there is no
			// logging channel here.
			_ = err
		}
	}()
	return server, nil
}

// suspend drops the live connection so an in-flight call fails instead of
// switching connections or replaying a mutation (Rust suspend).
func (s *taskToolsMCPServer) suspend() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connection = nil
}

// reconnect publishes the TUI connection again after a reconnect (Rust
// DynamicToolMcpServer::reconnect).
func (s *taskToolsMCPServer) reconnect(connection *taskToolsMCPConnection) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connection = connection
}

func (s *taskToolsMCPServer) currentConnection() (*taskToolsMCPConnection, error) {
	if s == nil {
		return nil, errors.New(taskToolsMCPReconnectingError)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return nil, errors.New(taskToolsMCPReconnectingError)
	}
	return s.connection, nil
}

// configValues are the thread config overrides that point the app server at this
// server (Rust ThreadToolTransport::configure_mcp).
func (s *taskToolsMCPServer) configValues() map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{"mcp_servers." + taskToolsMCPServerName: s.config}
}

func (s *taskToolsMCPServer) close() {
	if s == nil {
		return
	}
	if s.server != nil {
		_ = s.server.Close()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

type taskToolsMCPHandler struct {
	server *taskToolsMCPServer
}

func (h *taskToolsMCPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.server == nil {
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if request.URL.Path != taskToolsMCPPath {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	switch request.Method {
	case http.MethodDelete:
		writer.WriteHeader(http.StatusOK)
	case http.MethodPost:
		if !h.authorized(request) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.handlePost(writer, request)
	default:
		writer.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *taskToolsMCPHandler) authorized(request *http.Request) bool {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	return authorization != "" && authorization == h.server.token
}

type taskToolsMCPEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (h *taskToolsMCPHandler) handlePost(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, taskToolsMCPMaxRequestBytes))
	if err != nil {
		writeTaskToolsMCPError(writer, nil, -32700, "failed to read request")
		return
	}
	var envelope taskToolsMCPEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeTaskToolsMCPError(writer, nil, -32700, "invalid JSON")
		return
	}
	// Notifications carry no id and are acknowledged without a body.
	if len(envelope.ID) == 0 {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	result, rpcErr := h.dispatch(request.Context(), envelope.Method, envelope.Params)
	if rpcErr != nil {
		writeTaskToolsMCPError(writer, envelope.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	if result == nil {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(envelope.ID),
		"result":  result,
	})
}

type taskToolsMCPError struct {
	Code    int
	Message string
}

func (h *taskToolsMCPHandler) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *taskToolsMCPError) {
	switch method {
	case "initialize":
		protocolVersion := taskToolsMCPProtocolVersion
		var initialize struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(params, &initialize); err == nil && strings.TrimSpace(initialize.ProtocolVersion) != "" {
			protocolVersion = strings.TrimSpace(initialize.ProtocolVersion)
		}
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{
				"name":    taskToolsMCPServerName,
				"version": doctor.Version(),
			},
		}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": taskToolsMCPToolList()}, nil
	case "tools/call":
		result, rpcErr := h.callTool(ctx, params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return result, nil
	default:
		return nil, &taskToolsMCPError{Code: -32601, Message: "method not found"}
	}
}

// taskToolsMCPToolList mirrors Rust DynamicToolMcpHandler::list_tools: every
// function in the namespace, with a read-only annotation for the query tools.
func taskToolsMCPToolList() []map[string]any {
	tools := make([]map[string]any, 0, 9)
	for _, spec := range DynamicToolSpecs() {
		if spec.Namespace == nil {
			continue
		}
		for _, function := range spec.Namespace.Tools {
			tools = append(tools, map[string]any{
				"name":        function.Name,
				"description": function.Description,
				"inputSchema": function.InputSchema,
				"annotations": map[string]any{
					"readOnlyHint": taskToolsMCPReadOnlyTools[function.Name],
				},
			})
		}
	}
	return tools
}

func (h *taskToolsMCPHandler) callTool(ctx context.Context, params json.RawMessage) (any, *taskToolsMCPError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      map[string]any  `json:"_meta"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &taskToolsMCPError{Code: -32602, Message: "invalid tool call params"}
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return nil, &taskToolsMCPError{Code: -32602, Message: "tool name is required"}
	}
	connection, err := h.server.currentConnection()
	if err != nil {
		return nil, &taskToolsMCPError{Code: -32603, Message: err.Error()}
	}
	meta := taskToolsMCPMeta(call.Meta)
	threadID := stringFromAny(taskToolsMCPMetaValue(meta, "threadId"))
	if threadID == "" {
		return nil, &taskToolsMCPError{Code: -32602, Message: "missing task metadata"}
	}
	turnID := stringFromAny(taskToolsMCPMetaValue(meta, "turnId"))
	if turnID == "" {
		turnID = "mcp-turn-" + uuid.NewString()
	}
	callID := stringFromAny(taskToolsMCPMetaValue(meta, "callId"))
	if callID == "" {
		callID = "mcp-call-" + uuid.NewString()
	}
	var arguments any
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return nil, &taskToolsMCPError{Code: -32602, Message: "invalid tool arguments"}
		}
	}
	namespace := taskToolsMCPServerName
	response := ExecuteDynamicTool(ctx, connection.client, appserver.DynamicToolCallParams{
		ThreadID:  threadID,
		TurnID:    turnID,
		CallID:    callID,
		Namespace: &namespace,
		Tool:      name,
		Arguments: arguments,
	}, DynamicToolOptions{
		ThreadStartParams:        connection.template,
		RegisterBackgroundThread: connection.register,
	})
	content := make([]map[string]any, 0, len(response.ContentItems))
	for _, item := range response.ContentItems {
		switch item.Type {
		case "inputImage":
			content = append(content, map[string]any{"type": "text", "text": item.ImageURL})
		case "inputAudio":
			content = append(content, map[string]any{"type": "text", "text": item.AudioURL})
		default:
			content = append(content, map[string]any{"type": "text", "text": item.Text})
		}
	}
	return map[string]any{
		"content": content,
		"isError": !response.Success,
	}, nil
}

// taskToolsMCPMeta ports Rust's metadata lookup: the task fields come either
// directly from `_meta` or from its nested x-codex-turn-metadata document.
func taskToolsMCPMeta(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	combined := map[string]any{}
	for key, value := range meta {
		combined[key] = value
	}
	turnMetadata := meta["x-codex-turn-metadata"]
	switch typed := turnMetadata.(type) {
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(typed), &decoded); err == nil {
			turnMetadata = decoded
		}
	case map[string]any:
		// already decoded
	default:
		return combined
	}
	nested, ok := turnMetadata.(map[string]any)
	if !ok {
		return combined
	}
	for _, key := range []string{"threadId", "turnId", "callId"} {
		if _, exists := combined[key]; exists {
			continue
		}
		if value, exists := nested[key]; exists {
			combined[key] = value
			continue
		}
		snake := snakeCaseTaskToolsMCPKey(key)
		if value, exists := nested[snake]; exists {
			combined[key] = value
		}
	}
	return combined
}

func snakeCaseTaskToolsMCPKey(key string) string {
	switch key {
	case "threadId":
		return "thread_id"
	case "turnId":
		return "turn_id"
	case "callId":
		return "call_id"
	default:
		return key
	}
}

func taskToolsMCPMetaValue(meta map[string]any, key string) any {
	if meta == nil {
		return nil
	}
	return meta[key]
}

func writeTaskToolsMCPError(writer http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

// taskToolsMCPConfigName is the config key the app server resolves as the
// hosted task-tools server.
func taskToolsMCPConfigName() string {
	return fmt.Sprintf("mcp_servers.%s", taskToolsMCPServerName)
}

// taskToolsMCPHost keeps one local task-tools MCP server alive for the TUI
// session (Rust AppServerSession::dynamic_tool_mcp / ThreadToolTransport::Mcp).
// The listener URL stays stable so a thread's `mcp_servers.codex_tui` config
// keeps resolving across turns, while the live connection is swapped by each
// per-turn app-server client (Rust DynamicToolMcpServer::reconnect/suspend).
//
// A nil host disables the MCP transport, leaving the client on the app-server
// dynamic-tools callback transport (Rust ThreadToolTransport::Dynamic).
type taskToolsMCPHost struct {
	mu     sync.Mutex
	server *taskToolsMCPServer
}

// configValues returns the `mcp_servers.codex_tui` thread-config override, or
// nil while no server is running.
func (h *taskToolsMCPHost) configValues() map[string]any {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.server == nil {
		return nil
	}
	return h.server.configValues()
}

// attach (re)connects a live app-server client to the session's task-tools
// server, starting it on first use. The template carries the calling thread's
// config overrides plus the hosted server config, so tasks created through the
// namespace keep it (Rust DynamicToolMcpHandler::call_tool).
func (h *taskToolsMCPHost) attach(client *remoteAppServerTUIClient, template appserver.ThreadStartParams, register func(threadID string, taskToolsAvailable bool) error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	server := h.server
	if server == nil {
		started, err := startTaskToolsMCPServer(&taskToolsMCPConnection{})
		if err != nil {
			h.mu.Unlock()
			return
		}
		h.server = started
		server = started
	}
	h.mu.Unlock()
	// Rust DynamicToolMcpServer::start drops the web_search override from the
	// calling thread's config before hosting the namespace.
	if template.Config != nil {
		delete(template.Config, "web_search")
	}
	if overrides := server.configValues(); len(overrides) > 0 {
		if template.Config == nil {
			template.Config = map[string]any{}
		}
		for key, value := range overrides {
			template.Config[key] = value
		}
	}
	server.reconnect(&taskToolsMCPConnection{
		client:   client,
		template: template,
		register: register,
	})
}

// suspend drops the live connection so an in-flight call fails instead of
// switching connections or replaying a mutation (Rust suspend).
func (h *taskToolsMCPHost) suspend() {
	if h == nil {
		return
	}
	h.mu.Lock()
	server := h.server
	h.mu.Unlock()
	if server != nil {
		server.suspend()
	}
}

func (h *taskToolsMCPHost) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	server := h.server
	h.server = nil
	h.mu.Unlock()
	if server != nil {
		server.close()
	}
}
