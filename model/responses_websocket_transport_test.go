package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/codexapi"

	"github.com/coder/websocket"
)

type countingResponsesHTTPDoer struct {
	client *http.Client
	calls  atomic.Int32
}

func (d *countingResponsesHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return d.client.Do(request)
}

func isResponsesWebSocketUpgrade(request *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") ||
		strings.TrimSpace(request.Header.Get("Sec-WebSocket-Key")) != ""
}

func TestResponsesAgentRunnerRunPrefersWebSocketLikeRust(t *testing.T) {
	var websocketRequests atomic.Int32
	var httpRequests atomic.Int32
	headers := make(chan http.Header, 1)
	payloads := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !isResponsesWebSocketUpgrade(request) {
			httpRequests.Add(1)
			t.Errorf("Run sent an HTTP request while WebSocket support was enabled: %s %s", request.Method, request.URL)
			http.Error(w, "unexpected HTTP request", http.StatusInternalServerError)
			return
		}
		websocketRequests.Add(1)
		headers <- request.Header.Clone()
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("websocket.Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, data, err := conn.Read(request.Context())
		if err != nil {
			t.Errorf("websocket Read() error = %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Errorf("websocket payload Unmarshal() error = %v", err)
			return
		}
		payloads <- payload
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp-ws-run"}}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"msg-ws-run","type":"message","role":"assistant","content":[{"type":"output_text","text":"ws answer"}]}}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-ws-run","output":[{"id":"msg-ws-run","type":"message","role":"assistant","content":[{"type":"output_text","text":"ws answer"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
	}))
	defer server.Close()

	doer := &countingResponsesHTTPDoer{client: server.Client()}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		Stream:                  true,
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
		HTTPClient:              doer,
	})
	response, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:               "hello",
		Model:                "gpt-test",
		ThreadID:             "thread-ws-run",
		TurnID:               "turn-ws-run",
		ParallelToolCalls:    true,
		IncludeTimingMetrics: true,
		BetaFeaturesHeader:   "memories,remote_compaction_v2",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || response.ResponseID != "resp-ws-run" || response.Message != "ws answer" {
		t.Fatalf("response = %#v", response)
	}
	if got := websocketRequests.Load(); got != 1 {
		t.Fatalf("websocket requests = %d, want 1", got)
	}
	if got := httpRequests.Load(); got != 0 {
		t.Fatalf("HTTP POST requests = %d, want 0", got)
	}
	if got := doer.calls.Load(); got == 0 {
		t.Fatal("configured HTTPDoer was not used for the WebSocket handshake")
	}
	select {
	case payload := <-payloads:
		if payload["type"] != "response.create" || payload["stream"] != true || payload["model"] != "gpt-test" {
			t.Fatalf("websocket payload = %#v", payload)
		}
		if payload["parallel_tool_calls"] != true {
			t.Fatalf("parallel_tool_calls = %#v", payload["parallel_tool_calls"])
		}
	default:
		t.Fatal("websocket payload was not observed")
	}
	select {
	case got := <-headers:
		if got.Get(responsesIncludeTimingMetricsHeader) != "true" {
			t.Fatalf("timing metrics header = %q", got.Get(responsesIncludeTimingMetricsHeader))
		}
		if got.Get(codexapi.ClientCodexBetaFeaturesHeader) != "memories,remote_compaction_v2" {
			t.Fatalf("beta features header = %q", got.Get(codexapi.ClientCodexBetaFeaturesHeader))
		}
	default:
		t.Fatal("websocket handshake headers were not observed")
	}
}

func TestResponsesAgentRunnerRunFallsBackToHTTPOnUpgradeRequired(t *testing.T) {
	var websocketRequests atomic.Int32
	var httpRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if isResponsesWebSocketUpgrade(request) {
			websocketRequests.Add(1)
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		httpRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-http-fallback"}}`,
			`{"type":"response.output_item.done","item":{"id":"msg-http-fallback","type":"message","role":"assistant","content":[{"type":"output_text","text":"http answer"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-http-fallback","output":[{"id":"msg-http-fallback","type":"message","role":"assistant","content":[{"type":"output_text","text":"http answer"}]}]}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		Stream:                  true,
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
		HTTPClient:              server.Client(),
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || response.ResponseID != "resp-http-fallback" || response.Message != "http answer" {
		t.Fatalf("response = %#v", response)
	}
	if got := websocketRequests.Load(); got != 1 {
		t.Fatalf("websocket requests = %d, want 1", got)
	}
	if got := httpRequests.Load(); got != 1 {
		t.Fatalf("HTTP fallback requests = %d, want 1", got)
	}
	if !runner.websocketsDisabled() {
		t.Fatal("426 Upgrade Required did not disable WebSockets for later turns")
	}
}

func TestResponsesAgentRunnerRunUsesHTTPWithoutWebSocketSupport(t *testing.T) {
	var websocketRequests atomic.Int32
	var httpRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if isResponsesWebSocketUpgrade(request) {
			websocketRequests.Add(1)
			http.Error(w, "unexpected websocket", http.StatusInternalServerError)
			return
		}
		httpRequests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-http-only"}}`,
			`{"type":"response.completed","response":{"id":"resp-http-only","output":[{"id":"msg-http-only","type":"message","role":"assistant","content":[{"type":"output_text","text":"http only"}]}]}}`,
		)))
	}))
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:   &APIProvider{BaseURL: server.URL},
		Stream:     true,
		HTTPClient: server.Client(),
	})
	response, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response == nil || response.ResponseID != "resp-http-only" || response.Message != "http only" {
		t.Fatalf("response = %#v", response)
	}
	if got := websocketRequests.Load(); got != 0 {
		t.Fatalf("websocket requests = %d, want 0", got)
	}
	if got := httpRequests.Load(); got != 1 {
		t.Fatalf("HTTP requests = %d, want 1", got)
	}
}
