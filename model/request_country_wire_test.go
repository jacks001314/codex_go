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

	"codex_go/auth"
	"codex_go/protocol"
	"github.com/coder/websocket"
)

func mustRequestCountry(t *testing.T, code string) protocol.RequestCountry {
	t.Helper()
	country, err := protocol.ParseRequestCountry(code)
	if err != nil {
		t.Fatalf("ParseRequestCountry(%q) error = %v", code, err)
	}
	return country
}

// Mirrors Rust #61535: only a Codex-backend Responses request carries
// `request_country`; an omitted country leaves the field out, while the
// explicit unknown country serializes as null.
func TestResponsesAgentRunnerRequestCountryWireLikeRust(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{"id":"resp-country","model":"gpt-test","output_text":"ok"}`))
	}))
	t.Cleanup(server.Close)

	newRunner := func(snapshot *auth.AuthDotJSON) *ResponsesAgentRunner {
		return NewResponsesAgentRunner(&ResponsesAgentOptions{
			Provider:                   &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
			AuthSnapshot:               snapshot,
			ProviderRequiresOpenAIAuth: true,
			ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
				Slug: "gpt-test",
			}}}),
		})
	}
	run := func(runner *ResponsesAgentRunner, country protocol.RequestCountry) map[string]any {
		t.Helper()
		if _, err := runner.Run(context.Background(), &AgentRequest{
			Prompt: "hello", Model: "gpt-test", RequestCountry: country,
		}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		return bodies[len(bodies)-1]
	}

	chatGPT := newRunner(&auth.AuthDotJSON{AuthMode: "chatgpt"})
	body := run(chatGPT, mustRequestCountry(t, "de"))
	if body["request_country"] != "DE" {
		t.Fatalf("request_country = %#v, want DE", body["request_country"])
	}

	body = run(chatGPT, protocol.UnknownRequestCountry)
	value, present := body["request_country"]
	if !present || value != nil {
		t.Fatalf("explicit unknown request_country = %#v (present %v), want null", value, present)
	}

	body = run(chatGPT, protocol.RequestCountry{})
	if _, present := body["request_country"]; present {
		t.Fatalf("omitted request_country was sent: %#v", body["request_country"])
	}

	apiKey := newRunner(&auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"})
	body = run(apiKey, mustRequestCountry(t, "DE"))
	if _, present := body["request_country"]; present {
		t.Fatalf("API-key request carried request_country: %#v", body["request_country"])
	}
}

// countryWebsocketServer records each request's request_country and answers on
// the same socket so a reuse is observable.
type countryWebsocketServer struct {
	t          *testing.T
	mu         sync.Mutex
	handshakes int
	requests   int
	countries  []any
}

func (s *countryWebsocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.t.Errorf("websocket.Accept() error = %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")
	s.mu.Lock()
	s.handshakes++
	connection := s.handshakes
	s.mu.Unlock()
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(data, &payload)
		s.mu.Lock()
		s.requests++
		index := s.requests
		s.countries = append(s.countries, payload["request_country"])
		s.mu.Unlock()
		body := fmt.Sprintf(
			`{"type":"response.completed","response":{"id":"country-%d","output":[{"id":"m%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}},"connection":%d}`,
			index, index, connection)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(body))
	}
}

// Mirrors Rust #61535: a country change does not break WebSocket continuation
// reuse, and each response carries its own request_country.
func TestWebSocketContinuationReuseSurvivesCountryChangeLikeRust(t *testing.T) {
	server := &countryWebsocketServer{t: t}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                   &APIProvider{Name: OpenAIProviderName, BaseURL: httpServer.URL},
		Stream:                     true,
		SupportsWebsockets:         true,
		WebsocketConnectTimeout:    time.Second,
		AuthSnapshot:               &auth.AuthDotJSON{AuthMode: "chatgpt"},
		ProviderRequiresOpenAIAuth: true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := runner.RunWebSocket(ctx, &AgentRequest{
		Prompt: "one", Model: "gpt-test", ThreadID: "thread-country-socket",
		RequestCountry: mustRequestCountry(t, "DE"),
	})
	if err != nil || first == nil {
		t.Fatalf("first RunWebSocket() response=%#v err=%v", first, err)
	}
	second, err := runner.RunWebSocket(ctx, &AgentRequest{
		Prompt: "two", Model: "gpt-test", ThreadID: "thread-country-socket",
		PreviousResponseID: first.ResponseID,
		RequestCountry:     mustRequestCountry(t, "US"),
	})
	if err != nil || second == nil {
		t.Fatalf("second RunWebSocket() response=%#v err=%v", second, err)
	}
	server.mu.Lock()
	handshakes, requests, countries := server.handshakes, server.requests, append([]any(nil), server.countries...)
	server.mu.Unlock()
	if handshakes != 1 {
		t.Fatalf("handshakes = %d, want 1: a country change must keep the continuation", handshakes)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if len(countries) != 2 || countries[0] != "DE" || countries[1] != "US" {
		t.Fatalf("request_country values = %#v, want DE then US", countries)
	}
}
