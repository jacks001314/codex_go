package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/codexapi"
)

func TestResponsesEndpointPathsLikeRust(t *testing.T) {
	cases := map[ResponsesEndpoint]string{
		ResponsesEndpointResponses:          "/responses",
		ResponsesEndpointGuardian:           "/guardian",
		ResponsesEndpointGuardianClassifier: "/guardian-classifier",
	}
	for endpoint, want := range cases {
		if got := endpoint.Path(); got != want {
			t.Fatalf("%d.Path() = %q, want %q", endpoint, got, want)
		}
	}
}

func TestResponsesAgentRunnerUsesSelectedGuardianEndpoint(t *testing.T) {
	var recordedPath string
	var recordedBody map[string]any
	var routingHint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedPath = r.URL.Path
		routingHint = r.Header.Get(codexapi.ClientCodexRoutingHintHeader)
		if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	authHeaders := BearerAuthHeaders("sk-test", "", false)
	telemetry := &recordingTelemetrySink{}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1"},
		Auth:     &authHeaders,
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug: "gpt-test",
		}}}),
		ProviderID: OpenAIProviderID,
	})
	runner.Telemetry = telemetry
	_, err := runner.Run(context.Background(), &AgentRequest{
		Prompt:      "review",
		Model:       "gpt-test",
		Endpoint:    ResponsesEndpointGuardian,
		ServiceTier: "priority",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if recordedPath != "/v1/guardian" {
		t.Fatalf("request path = %q, want /v1/guardian", recordedPath)
	}
	if _, present := recordedBody["service_tier"]; present {
		t.Fatalf("dedicated Guardian request carried service_tier: %#v", recordedBody["service_tier"])
	}
	if routingHint != "" {
		t.Fatalf("dedicated Guardian request carried routing hint %q", routingHint)
	}
	if len(telemetry.apiRequests) != 1 || telemetry.apiRequests[0].Endpoint != "/guardian" {
		t.Fatalf("api request telemetry = %#v, want /guardian", telemetry.apiRequests)
	}
}
