package doctor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/model"
)

func TestChatGPTReachabilityTargetsCodexResponsesLikeRust(t *testing.T) {
	plan := providerReachabilityPlanFromParts(
		providerAuthReachabilityChatGPT,
		model.OpenAIProviderID,
		model.OpenAIProviderName,
		"",
		nil,
		false,
		"https://chatgpt.com/backend-api/",
	)
	if len(plan.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", plan.Endpoints)
	}
	endpoint := plan.Endpoints[0]
	if endpoint.Label != "ChatGPT" || endpoint.URL != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	if endpoint.RouteProbeURL != nil {
		t.Fatalf("ChatGPT endpoint should not probe the models route: %q", *endpoint.RouteProbeURL)
	}

	withQuery := providerReachabilityPlanFromParts(
		providerAuthReachabilityChatGPT,
		model.OpenAIProviderID,
		model.OpenAIProviderName,
		"",
		map[string]string{"api-version": "2026-01-01"},
		false,
		"https://chatgpt.com/backend-api",
	)
	if got, want := withQuery.Endpoints[0].URL, "https://chatgpt.com/backend-api/codex/responses?api-version=2026-01-01"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestProviderReachabilityRendersInferenceURLLikeRust(t *testing.T) {
	paths := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths[r.URL.Path] = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	plan := providerReachabilityPlanFromParts(
		providerAuthReachabilityAPIKey,
		model.OpenAIProviderID,
		model.OpenAIProviderName,
		server.URL+"/v1",
		nil,
		false,
		"https://chatgpt.com/backend-api/",
	)
	builder := NewBuilder()
	builder.httpClient = server.Client()
	check := builder.runProviderReachabilityCheck(plan)
	want := "openai API inference URL: " + server.URL + "/v1/responses reachable (HTTP 204)"
	if !containsDetail(check, want) {
		t.Fatalf("details = %#v", check.Details)
	}
	if !paths["/v1/responses"] {
		t.Fatalf("probe paths = %#v, want /v1/responses", paths)
	}
	for _, detail := range check.Details {
		if strings.Contains(detail, "base URL:") {
			t.Fatalf("stale base URL row rendered: %q", detail)
		}
	}
}
