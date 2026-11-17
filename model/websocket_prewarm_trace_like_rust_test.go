package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"codex_go/protocol"

	"github.com/coder/websocket"
)

const (
	startupPrewarmTraceparent  = "00-11111111111111111111111111111111-2222222222222222-01"
	turnTraceparent            = "00-33333333333333333333333333333333-4444444444444444-01"
	fallbackPrewarmTraceparent = "00-55555555555555555555555555555555-6666666666666666-01"
)

type traceHandshakeServer struct {
	t          *testing.T
	mu         sync.Mutex
	handshakes []http.Header
	payloads   []map[string]any
}

func (s *traceHandshakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.t.Errorf("Accept() error = %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	s.mu.Lock()
	s.handshakes = append(s.handshakes, r.Header.Clone())
	connection := len(s.handshakes)
	s.mu.Unlock()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			s.t.Errorf("Unmarshal() error = %v", unmarshalErr)
			return
		}
		s.mu.Lock()
		s.payloads = append(s.payloads, payload)
		s.mu.Unlock()
		generate := true
		if value, ok := payload["generate"].(bool); ok && !value {
			generate = false
		}
		if generate {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"resp-%d"}}`, connection)))
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d","output":[{"id":"note","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, connection)))
			continue
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d"}}`, connection)))
	}
}

func (s *traceHandshakeServer) snapshot() ([]http.Header, []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.handshakes...), append([]map[string]any(nil), s.payloads...)
}

func payloadClientMetadata(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	metadata, _ := payload["client_metadata"].(map[string]any)
	return metadata
}

// Rust parity: codex-rs/core/src/client.rs at #62103. The WebSocket handshake
// carries the current span's W3C trace context, the prewarm request keeps the
// startup trace, and a later turn on the same connection carries its own.
func TestResponsesWebsocketHandshakeCarriesSpanTraceLikeRust(t *testing.T) {
	server := &traceHandshakeServer{t: t}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: httpServer.URL},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	startupCtx := WithTelemetryTraceCarrier(context.Background(), &recordingTelemetrySpan{
		traceparent: startupPrewarmTraceparent,
		tracestate:  "startup=1",
	})
	if _, err := runner.Prewarm(startupCtx, &AgentRequest{Model: "gpt-test", ThreadID: "thread-trace"}); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if _, err := runner.RunWebSocket(context.Background(), &AgentRequest{
		Model:    "gpt-test",
		ThreadID: "thread-trace",
		Prompt:   "hi",
		Trace:    &protocol.W3CTraceContext{Traceparent: turnTraceparent, Tracestate: "turn=1"},
	}); err != nil {
		t.Fatalf("RunWebSocket() error = %v", err)
	}

	handshakes, payloads := server.snapshot()
	if len(handshakes) != 1 {
		t.Fatalf("handshakes = %d, want the turn to reuse the prewarmed socket", len(handshakes))
	}
	if got := handshakes[0].Get("traceparent"); got != startupPrewarmTraceparent {
		t.Fatalf("handshake traceparent = %q, want %q", got, startupPrewarmTraceparent)
	}
	if got := handshakes[0].Get("tracestate"); got != "startup=1" {
		t.Fatalf("handshake tracestate = %q, want startup=1", got)
	}
	if len(payloads) < 2 {
		t.Fatalf("payloads = %d, want a prewarm request and a turn request", len(payloads))
	}
	prewarmMetadata := payloadClientMetadata(payloads[0])
	if got := prewarmMetadata[websocketTraceparentMetadataKey]; got != startupPrewarmTraceparent {
		t.Fatalf("prewarm payload traceparent = %v, want %q", got, startupPrewarmTraceparent)
	}
	if got := prewarmMetadata[websocketTracestateMetadataKey]; got != "startup=1" {
		t.Fatalf("prewarm payload tracestate = %v, want startup=1", got)
	}
	turnMetadata := payloadClientMetadata(payloads[1])
	if got := turnMetadata[websocketTraceparentMetadataKey]; got != turnTraceparent {
		t.Fatalf("turn payload traceparent = %v, want its own %q", got, turnTraceparent)
	}
	if got := turnMetadata[websocketTracestateMetadataKey]; got != "turn=1" {
		t.Fatalf("turn payload tracestate = %v, want turn=1", got)
	}
}

// Rust parity: codex-rs/core/src/session/startup_prewarm.rs at #62103. When the
// prewarm span is filtered out the handshake falls back to the request trace.
func TestResponsesWebsocketHandshakeFallsBackToRequestTraceLikeRust(t *testing.T) {
	server := &traceHandshakeServer{t: t}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: httpServer.URL},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	ctx := protocol.WithTraceContext(context.Background(), &protocol.W3CTraceContext{
		Traceparent: fallbackPrewarmTraceparent,
		Tracestate:  "fallback=1",
	})
	if _, err := runner.Prewarm(ctx, &AgentRequest{Model: "gpt-test", ThreadID: "thread-fallback"}); err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}

	handshakes, payloads := server.snapshot()
	if len(handshakes) != 1 {
		t.Fatalf("handshakes = %d, want 1", len(handshakes))
	}
	if got := handshakes[0].Get("traceparent"); got != fallbackPrewarmTraceparent {
		t.Fatalf("handshake traceparent = %q, want %q", got, fallbackPrewarmTraceparent)
	}
	if got := handshakes[0].Get("tracestate"); got != "fallback=1" {
		t.Fatalf("handshake tracestate = %q, want fallback=1", got)
	}
	if len(payloads) == 0 {
		t.Fatal("prewarm payload missing")
	}
	if got := payloadClientMetadata(payloads[0])[websocketTraceparentMetadataKey]; got != fallbackPrewarmTraceparent {
		t.Fatalf("prewarm payload traceparent = %v, want the request fallback", got)
	}
}
