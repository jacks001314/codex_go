package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeRouterMCPEventStreamForwardsRequestMetaLikeRust mirrors Rust
// #41892 (and the app-server test suite): mcpServer/event/stream/start carries
// the caller's _meta through to the MCP events/stream params.
func TestRuntimeRouterMCPEventStreamForwardsRequestMetaLikeRust(t *testing.T) {
	metaSeen := make(chan map[string]any, 1)
	shutdown := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode MCP request error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"events": map[string]any{}},
				"serverInfo":      map[string]string{"name": "codex_apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "events/stream":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Errorf("unmarshal events/stream params error = %v", err)
				return
			}
			meta, _ := params["_meta"].(map[string]any)
			select {
			case metaSeen <- meta:
			default:
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/events/active\",\"params\":{}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-shutdown:
			case <-r.Context().Done():
			}
		default:
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer func() {
		close(shutdown)
		server.Close()
	}()

	store := session.NewStore(t.TempDir())
	mcpService := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		MCP:          mcpService,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(NewNotificationBuffer())
	t.Cleanup(func() { _ = router.Close() })

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-test", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = "conn-1"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	threadStart := requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()})
	threadStart.ConnectionID = "conn-1"
	response := router.Handle(threadStart)
	if response.Error != nil {
		t.Fatalf("thread start error: %+v", response.Error)
	}
	threadID := response.Result.(*ThreadStartResponse).Thread.ID

	start := requestWithParams(t, IntID(3), MethodMCPServerEventStreamStart, McpServerEventStreamStartParams{
		ThreadID:       threadID,
		Server:         "codex_apps",
		SubscriptionID: "sub-1",
		Name:           "fileChanged",
		Arguments:      json.RawMessage(`{"project":"codex"}`),
		Meta:           json.RawMessage(`{"source":"desktop"}`),
	})
	start.ConnectionID = "conn-1"
	if response := router.Handle(start); response.Error != nil {
		t.Fatalf("event stream start error: %+v", response.Error)
	}
	select {
	case meta := <-metaSeen:
		if meta["source"] != "desktop" {
			t.Fatalf("events/stream params _meta = %#v, want the caller meta", meta)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the MCP server never received events/stream")
	}

	stop := requestWithParams(t, IntID(4), MethodMCPServerEventStreamStop, McpServerEventStreamStopParams{SubscriptionID: "sub-1"})
	stop.ConnectionID = "conn-1"
	if response := router.Handle(stop); response.Error != nil {
		t.Fatalf("event stream stop error: %+v", response.Error)
	}
}
