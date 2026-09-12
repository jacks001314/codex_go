package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/turn"
)

type dynamicToolTestServer struct {
	mu       sync.Mutex
	methods  []appserver.Method
	turns    []turn.TurnStartParams
	forks    []appserver.ThreadForkParams
	names    []appserver.ThreadSetNameParams
	starts   []appserver.ThreadStartParams
	archives []string
	respond  func(method appserver.Method, params json.RawMessage) (any, error)
}

func (s *dynamicToolTestServer) record(method appserver.Method, params json.RawMessage) {
	s.methods = append(s.methods, method)
	switch method {
	case appserver.MethodTurnStart:
		var decoded turn.TurnStartParams
		_ = json.Unmarshal(params, &decoded)
		s.turns = append(s.turns, decoded)
	case appserver.MethodThreadFork:
		var decoded appserver.ThreadForkParams
		_ = json.Unmarshal(params, &decoded)
		s.forks = append(s.forks, decoded)
	case appserver.MethodThreadNameSet:
		var decoded appserver.ThreadSetNameParams
		_ = json.Unmarshal(params, &decoded)
		s.names = append(s.names, decoded)
	case appserver.MethodThreadStart:
		var decoded appserver.ThreadStartParams
		_ = json.Unmarshal(params, &decoded)
		s.starts = append(s.starts, decoded)
	case appserver.MethodThreadArchive, appserver.MethodThreadUnarchive:
		var decoded appserver.ThreadArchiveParams
		_ = json.Unmarshal(params, &decoded)
		s.archives = append(s.archives, decoded.ThreadID)
	}
}

func newDynamicToolClient(t *testing.T, server *dynamicToolTestServer) (*remoteAppServerTUIClient, func()) {
	t.Helper()
	ctx := context.Background()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				return
			}
			if req.Method == string(appserver.MethodInitialize) {
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				continue
			}
			server.mu.Lock()
			server.record(appserver.Method(req.Method), req.Params)
			respond := server.respond
			server.mu.Unlock()
			if respond == nil {
				t.Errorf("unexpected method %s", req.Method)
				return
			}
			result, err := respond(appserver.Method(req.Method), req.Params)
			if err != nil {
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32603, "message": err.Error()},
				})
				continue
			}
			remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}
	}))
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		httpServer.Close()
		t.Fatalf("client: %v", err)
	}
	return client, func() {
		client.close()
		httpServer.Close()
	}
}

func runDynamicTool(t *testing.T, server *dynamicToolTestServer, params appserver.DynamicToolCallParams, options DynamicToolOptions) appserver.DynamicToolCallResponse {
	t.Helper()
	client, cleanup := newDynamicToolClient(t, server)
	defer cleanup()
	return ExecuteDynamicTool(context.Background(), client, params, options)
}

func dynamicToolText(t *testing.T, response appserver.DynamicToolCallResponse) string {
	t.Helper()
	if len(response.ContentItems) == 0 {
		t.Fatal("response carried no content")
	}
	return response.ContentItems[0].Text
}

func TestDynamicToolSpecsExposeTaskNamespace(t *testing.T) {
	specs := DynamicToolSpecs()
	if len(specs) != 1 || specs[0].Namespace == nil {
		t.Fatalf("specs = %#v", specs)
	}
	namespace := specs[0].Namespace
	if namespace.Name != DynamicToolNamespace || namespace.Description == "" {
		t.Fatalf("namespace = %#v", namespace)
	}
	if len(namespace.Tools) != 9 {
		t.Fatalf("tools = %d, want 9", len(namespace.Tools))
	}
	seen := map[string]bool{}
	for _, tool := range namespace.Tools {
		seen[tool.Name] = true
		if !tool.DeferLoading || tool.InputSchema == nil {
			t.Fatalf("tool %q = %#v", tool.Name, tool)
		}
	}
	for _, name := range []string{
		"list_threads", "list_archived_threads", "read_thread", "wait_threads",
		"send_message_to_thread", "create_thread", "fork_thread",
		"set_thread_title", "set_thread_archived",
	} {
		if !seen[name] {
			t.Fatalf("missing tool %q in %#v", name, seen)
		}
	}

	nonDelegation := NonDelegationDynamicToolSpecs()
	if len(nonDelegation) != 1 || nonDelegation[0].Namespace == nil {
		t.Fatalf("non-delegation specs = %#v", nonDelegation)
	}
	for _, tool := range nonDelegation[0].Namespace.Tools {
		if dynamicDelegationTools[tool.Name] {
			t.Fatalf("delegation tool %q leaked into the filtered namespace", tool.Name)
		}
	}
	if len(nonDelegation[0].Namespace.Tools) != 6 {
		t.Fatalf("non-delegation tools = %d, want 6", len(nonDelegation[0].Namespace.Tools))
	}

	raw, err := DynamicToolSpecsRaw()
	if err != nil || len(raw) != 1 || !strings.Contains(string(raw[0]), `"namespace"`) {
		t.Fatalf("raw specs = %s err=%v", raw, err)
	}
	if err := turn.ValidateDynamicTools(specs); err != nil {
		t.Fatalf("specs are not a valid dynamic tool registration: %v", err)
	}
}

func TestExecuteDynamicToolListsThreads(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		if method != appserver.MethodThreadList {
			return nil, errors.New("unexpected method")
		}
		return map[string]any{"data": []any{
			map[string]any{"id": "thread-1", "preview": "fix the parser", "status": map[string]any{"type": "idle"}, "cwd": "/repo", "updatedAt": 7},
		}}, nil
	}}
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{Tool: "list_threads", Arguments: map[string]any{}}, DynamicToolOptions{})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	payload := dynamicToolText(t, response)
	for _, want := range []string{`"schemaVersion":4`, `"untrustedDataNotice"`, `"threads"`, `"status":"idle"`, `"summary":"fix the parser"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.methods) != 1 || server.methods[0] != appserver.MethodThreadList {
		t.Fatalf("methods = %#v", server.methods)
	}
}

func TestExecuteDynamicToolRejectsUnknownArguments(t *testing.T) {
	server := &dynamicToolTestServer{}
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		Tool:      "list_threads",
		Arguments: map[string]any{"bogus": true},
	}, DynamicToolOptions{})
	if response.Success || !strings.Contains(dynamicToolText(t, response), "Invalid tool arguments") {
		t.Fatalf("response = %#v", response)
	}
	response = runDynamicTool(t, server, appserver.DynamicToolCallParams{
		Tool:      "list_archived_threads",
		Arguments: map[string]any{"limit": 100},
	}, DynamicToolOptions{})
	if response.Success || !strings.Contains(dynamicToolText(t, response), "limit must be between 1 and 50") {
		t.Fatalf("response = %#v", response)
	}
}

func TestExecuteDynamicToolReadsThreadTurns(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadRead:
			return map[string]any{"thread": map[string]any{"id": "thread-1", "preview": "hi", "status": map[string]any{"type": "active", "activeFlags": []any{"waitingOnApproval"}}}}, nil
		case appserver.MethodThreadTurnsList:
			return map[string]any{"data": []any{
				map[string]any{"id": "turn-2", "status": "inProgress", "items": []any{
					map[string]any{"id": "a1", "type": "agentMessage", "text": "working"},
				}},
			}}, nil
		default:
			return nil, errors.New("unexpected method")
		}
	}}
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		Tool:      "read_thread",
		Arguments: map[string]any{"threadId": "thread-1"},
	}, DynamicToolOptions{})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	payload := dynamicToolText(t, response)
	for _, want := range []string{`"schemaVersion":1`, `"order":"newest_first"`, `"turn-2"`, `"agentMessage"`, `"working"`, `"status":"active"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
}

func TestExecuteDynamicToolSetsTitleAndArchives(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadNameSet, appserver.MethodThreadArchive, appserver.MethodThreadUnarchive:
			return map[string]any{}, nil
		default:
			return nil, errors.New("unexpected method")
		}
	}}
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "set_thread_title",
		Arguments: map[string]any{"title": "Renamed"},
	}, DynamicToolOptions{})
	if !response.Success || !strings.Contains(dynamicToolText(t, response), `"title":"Renamed"`) {
		t.Fatalf("response = %#v", response)
	}
	response = runDynamicTool(t, server, appserver.DynamicToolCallParams{
		Tool:      "set_thread_archived",
		Arguments: map[string]any{"threadId": "thread-1", "archived": true},
	}, DynamicToolOptions{})
	if !response.Success || !strings.Contains(dynamicToolText(t, response), `"archived":true`) {
		t.Fatalf("response = %#v", response)
	}
	// Archiving the calling task is rejected.
	response = runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "set_thread_archived",
		Arguments: map[string]any{"archived": true},
	}, DynamicToolOptions{})
	if response.Success || !strings.Contains(dynamicToolText(t, response), "cannot archive the calling task") {
		t.Fatalf("response = %#v", response)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.names) != 1 || server.names[0].Name != "Renamed" {
		t.Fatalf("names = %#v", server.names)
	}
	if len(server.archives) != 1 || server.archives[0] != "thread-1" {
		t.Fatalf("archives = %#v", server.archives)
	}
}

func TestExecuteDynamicToolSendMessageStartsToolTurn(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadRead:
			return map[string]any{"thread": map[string]any{"id": "thread-1", "status": map[string]any{"type": "idle"}}}, nil
		case appserver.MethodThreadResume:
			return map[string]any{"thread": map[string]any{"id": "thread-1"}}, nil
		case appserver.MethodTurnStart:
			return map[string]any{"turn": map[string]any{"id": "turn-new", "items": []any{}, "status": "inProgress"}}, nil
		default:
			return nil, errors.New("unexpected method")
		}
	}}
	var registered []string
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "send_message_to_thread",
		Arguments: map[string]any{"threadId": "thread-1", "prompt": "keep going", "model": "gpt-test"},
	}, DynamicToolOptions{
		RegisterBackgroundThread: func(threadID string, available bool) error {
			registered = append(registered, threadID)
			return nil
		},
	})
	if !response.Success || !strings.Contains(dynamicToolText(t, response), `"threadId":"thread-1"`) {
		t.Fatalf("response = %#v", response)
	}
	if len(registered) != 1 || registered[0] != "thread-1" {
		t.Fatalf("registered = %#v", registered)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.turns) != 1 {
		t.Fatalf("turns = %#v", server.turns)
	}
	start := server.turns[0]
	if start.ToolOutput == nil || start.ToolOutput.Name != "send_message_to_thread" || start.ToolOutput.Namespace != DynamicToolNamespace {
		t.Fatalf("tool output = %#v", start.ToolOutput)
	}
	if !strings.Contains(start.ToolOutput.Output, "<codex_delegation>") || !strings.Contains(start.ToolOutput.Output, "<source_thread_id>thread-caller</source_thread_id>") {
		t.Fatalf("delegated prompt = %q", start.ToolOutput.Output)
	}
	if strings.TrimSpace(start.Model) != "gpt-test" {
		t.Fatalf("model = %q", start.Model)
	}
}

func TestExecuteDynamicToolWaitSnapshot(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadRead:
			return map[string]any{"thread": map[string]any{"id": "thread-1", "status": map[string]any{"type": "idle"}, "updatedAt": 5}}, nil
		case appserver.MethodThreadTurnsList:
			return map[string]any{"data": []any{}}, nil
		case appserver.MethodThreadItemsList:
			return map[string]any{"data": []any{}}, nil
		default:
			return nil, errors.New("unexpected method")
		}
	}}
	now := time.Unix(0, 0)
	response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
		ThreadID:  "thread-caller",
		Tool:      "wait_threads",
		Arguments: map[string]any{"targets": []any{map[string]any{"threadId": "thread-1"}}, "timeoutMs": 0},
	}, DynamicToolOptions{
		Now:   func() time.Time { return now },
		Sleep: func(time.Duration) {},
	})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	payload := dynamicToolText(t, response)
	for _, want := range []string{`"timedOut":false`, `"reason":"inactiveStatus"`, `"polls"`, `"schemaVersion":1`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
}

func TestDynamicToolSuccessResponseTruncates(t *testing.T) {
	huge := strings.Repeat("x", 4000)
	response := dynamicToolSuccessResponse(map[string]any{
		"threads": []any{
			map[string]any{"id": "thread-1", "summary": huge, "status": "idle"},
			map[string]any{"id": "thread-2", "summary": huge, "status": "idle"},
		},
	})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	text := dynamicToolText(t, response)
	if len(text) > dynamicMaxResponseBytes {
		t.Fatalf("response bytes = %d, want <= %d", len(text), dynamicMaxResponseBytes)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Fatalf("response not marked truncated:\n%s", text)
	}
}

// TestRemoteServerRequestServesDynamicToolCall covers the wiring: the app
// server's item/tool/call request is served by the TUI's task-tool namespace
// instead of the previous "not available" error.
func TestRemoteServerRequestServesDynamicToolCall(t *testing.T) {
	server := &dynamicToolTestServer{respond: func(method appserver.Method, _ json.RawMessage) (any, error) {
		if method != appserver.MethodThreadList {
			return nil, errors.New("unexpected method")
		}
		return map[string]any{"data": []any{
			map[string]any{"id": "thread-1", "preview": "hello", "status": map[string]any{"type": "idle"}},
		}}, nil
	}}
	client, cleanup := newDynamicToolClient(t, server)
	defer cleanup()
	params, err := json.Marshal(appserver.DynamicToolCallParams{
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		Tool:      "list_threads",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, code, err := client.remoteServerRequestResult(context.Background(), appserver.ServerRequestDynamicToolCall, params)
	if err != nil || code != 0 {
		t.Fatalf("result=%#v code=%d err=%v", result, code, err)
	}
	response, ok := result.(appserver.DynamicToolCallResponse)
	if !ok || !response.Success {
		t.Fatalf("result = %#v", result)
	}
	if text := dynamicToolText(t, response); !strings.Contains(text, `"threads"`) {
		t.Fatalf("payload = %s", text)
	}
}

func TestExecuteDynamicToolRejectsUnknownTool(t *testing.T) {
	response := runDynamicTool(t, &dynamicToolTestServer{}, appserver.DynamicToolCallParams{Tool: "lookup"}, DynamicToolOptions{})
	if response.Success || !strings.Contains(dynamicToolText(t, response), "Unsupported TUI dynamic tool: lookup") {
		t.Fatalf("response = %#v", response)
	}
}

// TestRemoteThreadStartParamsRegisterTaskTools pins the transport: the TUI's
// thread starts expose the codex_tui namespace so the server can call back.
func TestRemoteThreadStartParamsRegisterTaskTools(t *testing.T) {
	params, err := remoteThreadStartParams(nil, nil)
	if err != nil {
		t.Fatalf("remoteThreadStartParams: %v", err)
	}
	if len(params.DynamicTools) != 1 {
		t.Fatalf("dynamicTools = %d, want 1", len(params.DynamicTools))
	}
	var spec struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params.DynamicTools[0], &spec); err != nil {
		t.Fatalf("unmarshal dynamic tool spec: %v", err)
	}
	if spec.Type != "namespace" || spec.Name != DynamicToolNamespace {
		t.Fatalf("spec = %#v", spec)
	}
}
