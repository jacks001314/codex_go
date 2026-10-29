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
	"codex_go/codexapi"
)

// These tests mirror Rust #47635: the startup prewarm handshake carries the
// effective routing hints and finalized tool metadata, recovers from
// authentication failures, and still falls back to HTTP on 426.

func prewarmWebsocketServer(t *testing.T, onConnect func(request *http.Request, payload map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, data, err := conn.Read(request.Context())
		if err != nil {
			t.Errorf("Read() error = %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
			return
		}
		if onConnect != nil {
			onConnect(request, payload)
		}
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"warm-1"}}`))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"warm-1"}}`))
	}))
}

func TestResponsesAgentRunnerPrewarmCarriesServiceTierRoutingLikeRust(t *testing.T) {
	var routingHint string
	var payload map[string]any
	server := prewarmWebsocketServer(t, func(request *http.Request, received map[string]any) {
		routingHint = request.Header.Get(codexapi.ClientCodexRoutingHintHeader)
		payload = received
	})
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL},
		AuthSnapshot:            &auth.AuthDotJSON{AuthMode: "chatgpt"},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	response, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-6.1-sol", ServiceTier: "priority"})
	if err != nil || response == nil || response.ResponseID != "warm-1" {
		t.Fatalf("Prewarm() response=%#v err=%v", response, err)
	}
	if routingHint != "model=gpt-6.1-sol;tier=priority" {
		t.Fatalf("routing hint = %q", routingHint)
	}
	if payload["service_tier"] != "priority" || payload["generate"] != false {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestResponsesAgentRunnerPrewarmOmitsGuardianRoutingLikeRust(t *testing.T) {
	var routingHint string
	var guardianHeader string
	var payload map[string]any
	server := prewarmWebsocketServer(t, func(request *http.Request, received map[string]any) {
		routingHint = request.Header.Get(codexapi.ClientCodexRoutingHintHeader)
		guardianHeader = request.Header.Get(GuardianHeaderName)
		payload = received
	})
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL},
		AuthSnapshot:            &auth.AuthDotJSON{AuthMode: "chatgpt"},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	_, err := runner.Prewarm(context.Background(), &AgentRequest{
		Model:       DefaultApprovalReviewPreferredModel,
		TaskKind:    AgentTaskReview,
		ServiceTier: "priority",
	})
	if err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if routingHint != "" {
		t.Fatalf("guardian routing hint = %q, want empty", routingHint)
	}
	if guardianHeader != GuardianHeaderReviewer {
		t.Fatalf("guardian header = %q", guardianHeader)
	}
	if _, ok := payload["service_tier"]; ok {
		t.Fatalf("guardian payload carried a service tier: %#v", payload)
	}
}

func TestResponsesAgentRunnerPrewarmRecoversAuthAfter401LikeRust(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts++
		if attempts == 1 {
			if got := request.Header.Get("Authorization"); got != "Bearer old-token" {
				t.Fatalf("first auth = %q", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer new-token" {
			t.Fatalf("second auth = %q", got)
		}
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"warm-refreshed"}}`))
	}))
	defer server.Close()

	plan := "pro"
	snapshot := auth.FromChatGPTAuthTokens("old-token", "account-old", &plan)
	headers := BearerAuthHeaders("old-token", "account-old", false)
	refreshCalls := 0
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: server.URL},
		SupportsWebsockets: true,
		Auth:               &headers,
		AuthSnapshot:       &snapshot,
		ExternalAuthRefresh: func(context.Context, *ExternalAuthRefreshRequest) (*ExternalAuthRefreshResponse, error) {
			refreshCalls++
			return &ExternalAuthRefreshResponse{AccessToken: "new-token", ChatGPTAccountID: "account-new", ChatGPTPlanType: &plan}, nil
		},
	})
	response, err := runner.Prewarm(context.Background(), &AgentRequest{Model: "gpt-test"})
	if err != nil || response == nil || response.ResponseID != "warm-refreshed" {
		t.Fatalf("Prewarm() response=%#v err=%v", response, err)
	}
	if attempts != 2 || refreshCalls != 1 {
		t.Fatalf("attempts=%d refreshCalls=%d", attempts, refreshCalls)
	}
}

func TestResponsesAgentRunnerPrewarmCarriesFinalizedToolMetadataLikeRust(t *testing.T) {
	var payload map[string]any
	server := prewarmWebsocketServer(t, func(_ *http.Request, received map[string]any) {
		payload = received
	})
	defer server.Close()

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                &APIProvider{BaseURL: server.URL},
		SupportsWebsockets:      true,
		WebsocketConnectTimeout: time.Second,
	})
	_, err := runner.Prewarm(context.Background(), &AgentRequest{
		Model:    "gpt-6.1-sol",
		ThreadID: "thread-prewarm-tools",
		Tools: []any{map[string]any{
			"type":        "function",
			"name":        "lookup",
			"description": "Look something up",
			"parameters":  map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("Prewarm() error = %v", err)
	}
	if _, ok := payload["tools"]; ok {
		t.Fatalf("Responses Lite prewarm sent a top-level tools array: %#v", payload)
	}
	encoded, _ := json.Marshal(payload["input"])
	if !strings.Contains(string(encoded), `"additional_tools"`) || !strings.Contains(string(encoded), `"lookup"`) {
		t.Fatalf("prewarm input did not carry the finalized tool declarations: %s", encoded)
	}
}
