package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/auth"
)

// prewarmToolCaptureServer accepts the websocket upgrade, then returns the
// generate=false payload the warmup sent and closes the exchange. The handshake
// signal lets a provider prove the upgrade preceded tool capture.
func prewarmToolCaptureServer(t *testing.T, payload *map[string]any, handshakeAccepted chan<- struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		close(handshakeAccepted)
		_, data, err := conn.Read(request.Context())
		if err != nil {
			t.Errorf("Read() error = %v", err)
			return
		}
		if err := json.Unmarshal(data, payload); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
			return
		}
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"warm-tools"}}`))
	}))
}

// TestResponsesAgentRunnerPrewarmOverlapsToolCaptureLikeRust mirrors Rust
// #47635: the websocket handshake completes before tool capture does, and the
// captured tools join the generate=false warmup payload.
func TestResponsesAgentRunnerPrewarmOverlapsToolCaptureLikeRust(t *testing.T) {
	discovered := func() any {
		return map[string]any{
			"type":        "function",
			"name":        "discovered_mcp_tool",
			"description": "Discovered after the handshake",
			"parameters":  map[string]any{"type": "object"},
		}
	}
	cases := []struct {
		name  string
		model string
	}{
		{name: "legacy model sends a top-level tools array", model: "gpt-test"},
		{name: "Responses Lite sends additional_tools declarations", model: "gpt-6.1-sol"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handshakeAccepted := make(chan struct{})
			var payload map[string]any
			server := prewarmToolCaptureServer(t, &payload, handshakeAccepted)
			defer server.Close()

			runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
				Provider:                &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL},
				AuthSnapshot:            &auth.AuthDotJSON{AuthMode: "chatgpt"},
				SupportsWebsockets:      true,
				WebsocketConnectTimeout: 5 * time.Second,
			})
			response, err := runner.Prewarm(context.Background(), &AgentRequest{
				Model:    testCase.model,
				ThreadID: "thread-prewarm-tool-capture",
				PrewarmTools: func(ctx context.Context) []any {
					select {
					case <-handshakeAccepted:
					case <-time.After(3 * time.Second):
						t.Error("handshake did not complete before tool capture")
					}
					return []any{discovered()}
				},
			})
			if err != nil || response == nil || response.ResponseID != "warm-tools" {
				t.Fatalf("Prewarm() response=%#v err=%v", response, err)
			}
			encoded, _ := json.Marshal(payload)
			if !strings.Contains(string(encoded), "discovered_mcp_tool") {
				t.Fatalf("warmup payload did not carry the captured tool: %s", encoded)
			}
			if testCase.model == "gpt-6.1-sol" {
				if _, ok := payload["tools"]; ok {
					t.Fatalf("Responses Lite prewarm sent a top-level tools array: %s", encoded)
				}
				if !strings.Contains(string(encoded), `"additional_tools"`) {
					t.Fatalf("Responses Lite warmup did not declare additional_tools: %s", encoded)
				}
			}
		})
	}
}
