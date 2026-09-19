package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// mcpRequestMetadataAgent issues one MCP tool call whose Responses item carries
// an id, then reports the turn complete.
type mcpRequestMetadataAgent struct {
	requests  chan model.AgentRequest
	calls     int
	namespace string
	tool      string
	itemID    string
	promise   chan struct{}
}

func newMCPRequestMetadataAgent() *mcpRequestMetadataAgent {
	return &mcpRequestMetadataAgent{requests: make(chan model.AgentRequest, 4), promise: make(chan struct{})}
}

func (a *mcpRequestMetadataAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{ResponseID: "resp-mcp-request-metadata", Items: []model.AgentItem{{
			ID:        a.itemID,
			Type:      "function_call",
			Name:      a.tool,
			Namespace: a.namespace,
			CallID:    "mcp-meta-call",
			Arguments: `{"query":"redaction"}`,
		}}}, nil
	}
	return &model.AgentResponse{ResponseID: "resp-mcp-request-metadata-done", Message: "mcp done", Items: []model.AgentItem{{
		ID:   "msg-after-mcp-request-metadata",
		Type: "agent_message",
		Text: "mcp done",
	}}}, nil
}

func waitForMCPRequestMetadataRequest(t *testing.T, agent *mcpRequestMetadataAgent) model.AgentRequest {
	t.Helper()
	select {
	case request := <-agent.requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the MCP request-metadata runtime request")
		return model.AgentRequest{}
	}
}

// TestRuntimeRouterMCPRequestMetadataReportsCallIDsLikeRust mirrors Rust
// with_mcp_tool_call_ids_meta (#40866/#45409): an MCP request reports
// `threadId`, the session identity, the originating window and the Responses
// item that requested the call. The window id is the same
// `{thread_id}:{window_number}` identity the turn's Responses client metadata
// reports, and the item id is the call item's id - not the tool call id.
func TestRuntimeRouterMCPRequestMetadataReportsCallIDsLikeRust(t *testing.T) {
	var mu sync.Mutex
	var toolCalls []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode MCP request error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-mcp-request-metadata")
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "codex_apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{
				codexAppsCatalogTool("calendar_list_events", "calendar"),
			}})
		case "resources/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		case "tools/call":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("Unmarshal tools/call params error = %v", err)
			}
			mu.Lock()
			toolCalls = append(toolCalls, params)
			mu.Unlock()
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]string{{"type": "text", "text": "calendar ok"}},
				"isError": false,
			})
		default:
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer server.Close()

	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nexecuted_tool_call_metadata = true\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	mcpService := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
	}})
	agent := newMCPRequestMetadataAgent()
	agent.itemID = "ctc_before_compaction"
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		MCP:          mcpService,
		DefaultCWD:   cwd,
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	thread := threadStart.Result.(*ThreadStartResponse).Thread

	tools, _ := router.mcpRuntimeInputsForServiceWithRequirements(thread.ID, nil, mcpService, nil, nil)
	if len(tools) != 1 {
		t.Fatalf("MCP tools = %#v, want one calendar tool", tools)
	}
	agent.namespace = strings.TrimPrefix(tools[0].CallableNamespace, mcp.LegacyMCPToolNamePrefix)
	agent.tool = tools[0].CallableName

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: thread.ID,
		Prompt:   "list my calendar events",
		CWD:      cwd,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForItemCompletedType(t, sink, "mcp-meta-call", "mcpToolCall")
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
	_ = waitForMCPRequestMetadataRequest(t, agent)
	_ = waitForMCPRequestMetadataRequest(t, agent)

	mu.Lock()
	calls := append([]map[string]any(nil), toolCalls...)
	mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("tools/call params = %#v, want one call", calls)
	}
	params := calls[0]
	if params["name"] != "calendar_list_events" {
		t.Fatalf("tools/call name = %#v", params["name"])
	}
	meta, ok := params["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call _meta = %#v", params["_meta"])
	}
	// The window identity is the turn's conversation window: Rust numbers it
	// from zero and reports it as `x-codex-window-id` too.
	wantWindowID := thread.ID + ":0"
	for key, want := range map[string]any{
		"threadId":  thread.ID,
		"sessionId": thread.SessionID,
		"windowId":  wantWindowID,
		"itemId":    agent.itemID,
		"callId":    "mcp-meta-call",
	} {
		if meta[key] != want {
			t.Fatalf("_meta[%s] = %#v, want %#v (meta=%#v)", key, meta[key], want, meta)
		}
	}
	if thread.SessionID == "" {
		t.Fatalf("thread start did not report a session id: %#v", thread)
	}
}
