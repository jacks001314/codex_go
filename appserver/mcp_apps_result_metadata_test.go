package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// mcpAppsResultMetadataAgent issues one MCP tool call and then reports the turn
// complete, keeping every agent request so the recorded executed-tool-call
// metadata of the tool output can be inspected on the follow-up request.
type mcpAppsResultMetadataAgent struct {
	requests  chan model.AgentRequest
	calls     int
	namespace string
	tool      string
}

func newMCPAppsResultMetadataAgent() *mcpAppsResultMetadataAgent {
	return &mcpAppsResultMetadataAgent{requests: make(chan model.AgentRequest, 4)}
}

func (a *mcpAppsResultMetadataAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	a.calls++
	if a.calls == 1 {
		return &model.AgentResponse{ResponseID: "resp-mcp-result-metadata", Items: []model.AgentItem{{
			ID:        "mcp-result-metadata-call",
			Type:      "function_call",
			Name:      a.tool,
			Namespace: a.namespace,
			CallID:    "mcp-result-metadata-call",
			Arguments: `{"query":"redaction"}`,
		}}}, nil
	}
	return &model.AgentResponse{ResponseID: "resp-mcp-result-metadata-done", Message: "mcp done", Items: []model.AgentItem{{
		ID:   "msg-after-mcp-result-metadata",
		Type: "agent_message",
		Text: "mcp done",
	}}}, nil
}

func waitForMCPResultMetadataRequest(t *testing.T, agent *mcpAppsResultMetadataAgent) model.AgentRequest {
	t.Helper()
	select {
	case request := <-agent.requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the MCP result-metadata runtime request")
		return model.AgentRequest{}
	}
}

// TestRuntimeRouterMCPResultMetadataFollowsAnalyticsLikeRust mirrors Rust
// #46010's `result_metadata_follows_call_binding`: the raw MCP result `_meta` is
// recorded on the executed tool call only when the session's analytics client is
// enabled and the call belongs to the host-owned apps server. A non-apps server
// never records it, and a disabled analytics client suppresses it for an apps
// call. The model-visible metadata is a separate surface, so the capture gate
// must not change the tool output itself.
func TestRuntimeRouterMCPResultMetadataFollowsAnalyticsLikeRust(t *testing.T) {
	resultMetadata := map[string]any{"provider/custom": map[string]any{"items": []any{1, nil}}}
	cases := []struct {
		name        string
		serverName  string
		analytics   bool
		wantCapture bool
	}{
		{name: "hosted apps with analytics enabled", serverName: mcp.RuntimeCodexAppsMCPServerName, analytics: true, wantCapture: true},
		{name: "hosted apps with analytics disabled", serverName: mcp.RuntimeCodexAppsMCPServerName},
		{name: "external server keeps the result metadata internal", serverName: "sdk", analytics: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runMCPResultMetadataTurn(t, testCase.serverName, testCase.analytics, testCase.wantCapture, resultMetadata)
		})
	}
}

func runMCPResultMetadataTurn(t *testing.T, serverName string, analyticsEnabled bool, wantCapture bool, resultMetadata map[string]any) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode MCP request error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-mcp-result-metadata")
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": serverName, "version": "test"},
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
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]string{{"type": "text", "text": "calendar ok"}},
				"isError": false,
				"_meta":   resultMetadata,
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
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	mcpService := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		serverName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
	}})
	agent := newMCPAppsResultMetadataAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Analytics:    enabledAnalyticsSink{enabled: analyticsEnabled},
		MCP:          mcpService,
		DefaultCWD:   cwd,
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: cwd}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	tools, _ := router.mcpRuntimeInputsForServiceWithRequirements(threadID, nil, mcpService, nil, nil)
	if len(tools) != 1 {
		t.Fatalf("MCP tools = %#v, want one calendar tool", tools)
	}
	agent.namespace = strings.TrimPrefix(tools[0].CallableNamespace, mcp.LegacyMCPToolNamePrefix)
	agent.tool = tools[0].CallableName
	if agent.namespace == "" || agent.tool == "" {
		t.Fatalf("callable MCP tool name = %q.%q", agent.namespace, agent.tool)
	}

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "list my calendar events",
		CWD:      cwd,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	completed := waitForItemCompletedType(t, sink, "mcp-result-metadata-call", "mcpToolCall")
	if _, leaked := completed.Item["tool_result_metadata"]; leaked {
		t.Fatalf("the raw result metadata leaked into the item notification: %#v", completed.Item)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	_ = waitForMCPResultMetadataRequest(t, agent)
	followUp := waitForMCPResultMetadataRequest(t, agent)
	var output *turn.ToolResponseItem
	for _, input := range followUp.InputItems {
		if item, ok := input.(*turn.ToolResponseItem); ok && item.CallID == "mcp-result-metadata-call" {
			output = item
			break
		}
	}
	if output == nil {
		t.Fatalf("MCP tool output missing from the follow-up request: %#v", followUp.InputItems)
	}
	calls := output.ExecutedToolCalls()
	if len(calls) != 1 {
		t.Fatalf("executed tool calls = %#v, want the apps tool call", calls)
	}
	encoded, err := json.Marshal(calls[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var recorded map[string]any
	if err := json.Unmarshal(encoded, &recorded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	_, hasMetadata := recorded["tool_result_metadata"]
	if hasMetadata != wantCapture {
		t.Fatalf("recorded result metadata present = %v, want %v: %s", hasMetadata, wantCapture, encoded)
	}
	if wantCapture && !strings.Contains(string(encoded), `"provider/custom"`) {
		t.Fatalf("recorded result metadata lost its payload: %s", encoded)
	}
}
