package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"codex_go/network"
)

// Mirrors Rust #57710 for the Go transport: a Guardian reviewer dial merges the
// captured parent routing pin into its handshake, even after the shared cookie
// jar no longer carries it, and a changed pin reconnects the cached socket.
func TestGuardianWebsocketRoutingAffinityLikeRust(t *testing.T) {
	const host = "guardian-affinity.chatgpt.com"
	target, err := url.Parse("https://" + host + "/v1/")
	if err != nil {
		t.Fatal(err)
	}
	clearRoutingCookie := func() {
		network.SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{
			Name: "__oailb", Value: "", Path: "/", MaxAge: -1, Secure: true,
		}})
	}
	clearRoutingCookie()
	t.Cleanup(clearRoutingCookie)

	var mu sync.Mutex
	var cookies []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		cookies = append(cookies, request.Header.Get("Cookie"))
		mu.Unlock()
		edge := "parent-edge"
		if len(request.Header.Get("Cookie")) > 0 && request.Header.Get("Cookie") != "__oailb=parent-edge" {
			edge = "review-edge"
		}
		http.SetCookie(w, &http.Cookie{Name: "__oailb", Value: edge, Path: "/", Secure: true})
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"resp-routing"}}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-routing","output":[{"id":"msg-routing","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`))
	}))
	defer server.Close()

	options := &ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: "https://" + host + "/v1"},
		HTTPClient:         chatGPTCookieRoutedClient(server),
		SupportsWebsockets: true,
	}
	runner := NewResponsesAgentRunner(options)

	var createdAffinity *network.RoutingAffinity
	parentRequest := &AgentRequest{
		Model: "gpt-test", Prompt: "hello",
		ThreadID: "thread-routing", TurnID: "turn-parent",
		StreamHandler: func(event *ResponsesStreamEvent) {
			if event.Kind == ResponsesStreamEventCreated {
				createdAffinity = event.RoutingAffinity
			}
		},
	}
	parent, err := runner.RunWebSocket(context.Background(), parentRequest)
	if err != nil || parent == nil {
		t.Fatalf("parent RunWebSocket: response=%#v err=%v", parent, err)
	}
	if parent.RoutingAffinity == nil {
		t.Fatal("parent response has no routing affinity")
	}
	if createdAffinity != parent.RoutingAffinity {
		t.Fatalf("response.created affinity = %v, want the handshake pin %v", createdAffinity, parent.RoutingAffinity)
	}
	clearRoutingCookie()

	review := func(turnID string, affinity *network.RoutingAffinity) *AgentResponse {
		t.Helper()
		response, err := runner.RunWebSocket(context.Background(), &AgentRequest{
			Model: "gpt-test", Prompt: "review", Originator: "guardian",
			TaskKind: AgentTaskReview, ThreadID: "thread-routing", TurnID: turnID,
			RoutingAffinity: affinity,
		})
		if err != nil || response == nil {
			t.Fatalf("review RunWebSocket(%s): response=%#v err=%v", turnID, response, err)
		}
		return response
	}
	reviewResponse := review("turn-review", parent.RoutingAffinity)
	mu.Lock()
	got := append([]string(nil), cookies...)
	mu.Unlock()
	if len(got) != 2 || got[1] != "__oailb=parent-edge" {
		t.Fatalf("review handshakes = %#v, want the captured parent pin", got)
	}
	if reviewResponse.RoutingAffinity == nil {
		t.Fatal("review response has no routing affinity")
	}

	// A different parent pin reconnects the cached reviewer socket.
	otherHeaders := http.Header{}
	otherHeaders.Set("Set-Cookie", "__oailb=other-edge; Path=/; Secure")
	other := network.RoutingAffinityFromResponse("wss://"+host+"/v1/responses", otherHeaders)
	if other == nil {
		t.Fatal("failed to capture the second pin")
	}
	clearRoutingCookie()
	review("turn-review", other)
	mu.Lock()
	got = append([]string(nil), cookies...)
	mu.Unlock()
	if len(got) != 3 || got[2] != "__oailb=other-edge" {
		t.Fatalf("changed pin handshakes = %#v, want a reconnect with the new pin", got)
	}

	// An ordinary request never applies a reviewer pin.
	clearRoutingCookie()
	if _, err := runner.RunWebSocket(context.Background(), &AgentRequest{
		Model: "gpt-test", Prompt: "hello",
		ThreadID: "thread-routing", TurnID: "turn-ordinary",
		RoutingAffinity: other,
	}); err != nil {
		t.Fatalf("ordinary RunWebSocket error = %v", err)
	}
	mu.Lock()
	got = append([]string(nil), cookies...)
	mu.Unlock()
	if len(got) != 4 || got[3] != "" {
		t.Fatalf("ordinary handshakes = %#v, want no reviewer pin", got)
	}
}
