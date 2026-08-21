package appserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #39761: mcpServer/event/stream/start|stop forward hosted-app MCP event
// notifications to the owning connection, reject duplicate subscriptions, and
// clean up on unsubscribe/stop.
func TestRuntimeRouterMCPEventStreamStartStopForwardsAndScopesLikeRust(t *testing.T) {
	release := make(chan struct{})
	var eventsMu sync.Mutex
	eventStreamRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode MCP request error = %v", err)
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
			eventsMu.Lock()
			eventStreamRequests++
			eventsMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			writeSSE := func(method string, params string) {
				_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":%q,\"params\":%s}\n\n", method, params)
				if flusher != nil {
					flusher.Flush()
				}
			}
			writeSSE("notifications/events/active", `{}`)
			writeSSE("notifications/events/fileChanged", `{"name":"fileChanged"}`)
			<-release
		default:
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	mcpService := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		MCP:          mcpService,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{
			ExperimentalAPI: true,
		},
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
		Arguments:      json.RawMessage(`{}`),
	})
	start.ConnectionID = "conn-1"
	response = router.Handle(start)
	if response.Error != nil {
		t.Fatalf("event stream start error: %+v", response.Error)
	}

	eventsMu.Lock()
	gotRequests := eventStreamRequests
	eventsMu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("events/stream requests = %d, want 1", gotRequests)
	}

	forwarded := false
	for _, notification := range sink.List() {
		if notification.Method != NotificationMCPServerEventStream {
			continue
		}
		payload, ok := notification.Params.(*McpServerEventStreamNotification)
		if !ok {
			t.Fatalf("event stream notification params type = %T", notification.Params)
		}
		if payload.SubscriptionID != "sub-1" {
			t.Fatalf("subscription id = %q", payload.SubscriptionID)
		}
		if payload.Notification.Method == "notifications/events/fileChanged" {
			forwarded = true
			var params map[string]any
			if err := json.Unmarshal(payload.Notification.Params, &params); err != nil || params["name"] != "fileChanged" {
				t.Fatalf("forwarded params = %s err=%v", payload.Notification.Params, err)
			}
		}
	}
	if !forwarded {
		t.Fatalf("fileChanged event not forwarded; notifications = %+v", sink.List())
	}

	duplicate := requestWithParams(t, IntID(4), MethodMCPServerEventStreamStart, McpServerEventStreamStartParams{
		ThreadID:       threadID,
		Server:         "codex_apps",
		SubscriptionID: "sub-1",
		Name:           "fileChanged",
		Arguments:      json.RawMessage(`{}`),
	})
	duplicate.ConnectionID = "conn-1"
	response = router.Handle(duplicate)
	if response.Error == nil || !strings.Contains(response.Error.Message, "already exists") {
		t.Fatalf("duplicate event stream start response = %+v", response)
	}

	stop := requestWithParams(t, IntID(5), MethodMCPServerEventStreamStop, McpServerEventStreamStopParams{SubscriptionID: "sub-1"})
	stop.ConnectionID = "conn-1"
	response = router.Handle(stop)
	if response.Error != nil {
		t.Fatalf("event stream stop error: %+v", response.Error)
	}

	router.mcpEventStreams.mu.Lock()
	remaining := len(router.mcpEventStreams.tasks)
	router.mcpEventStreams.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("event streams remaining after stop = %d", remaining)
	}
}

func TestRuntimeRouterMCPEventStreamRejectsNonHostedServerAndBadThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        newRecordingRuntimeAgent("ok"),
		MCP:          mcp.NewMCPService(nil),
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	nonHosted := requestWithParams(t, IntID(1), MethodMCPServerEventStreamStart, McpServerEventStreamStartParams{
		ThreadID:       "00000000-0000-0000-0000-000000000000",
		Server:         "docs",
		SubscriptionID: "sub-1",
		Name:           "fileChanged",
		Arguments:      json.RawMessage(`{}`),
	})
	response := router.Handle(nonHosted)
	if response.Error == nil || !strings.Contains(response.Error.Message, "only supported for hosted apps") {
		t.Fatalf("non-hosted response = %+v", response)
	}

	badThread := requestWithParams(t, IntID(2), MethodMCPServerEventStreamStart, McpServerEventStreamStartParams{
		ThreadID:       "not-a-uuid",
		Server:         "codex_apps",
		SubscriptionID: "sub-1",
		Name:           "fileChanged",
		Arguments:      json.RawMessage(`{}`),
	})
	response = router.Handle(badThread)
	if response.Error == nil || !strings.Contains(response.Error.Message, "invalid thread id") {
		t.Fatalf("bad thread response = %+v", response)
	}
}
