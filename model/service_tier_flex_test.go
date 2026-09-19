package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Mirrors Rust #46230's get_service_tier: a configured flex tier is preserved
// without fast mode or catalog support, while every other tier still requires
// fast mode and catalog support.
func TestServiceTierForConfiguredRequestLikeRust(t *testing.T) {
	supported := &ModelInfo{ServiceTiers: []string{"priority", "flex"}}
	for _, fastModeEnabled := range []bool{false, true} {
		if got := ServiceTierForConfiguredRequest(supported, "flex", fastModeEnabled); got != "flex" {
			t.Fatalf("flex (fast_mode=%v) = %q, want flex", fastModeEnabled, got)
		}
	}
	// Flex survives a catalog that does not advertise it.
	noTiers := &ModelInfo{}
	if got := ServiceTierForConfiguredRequest(noTiers, "flex", false); got != "flex" {
		t.Fatalf("flex without catalog support = %q, want flex", got)
	}
	// Ordinary tiers still need fast mode and support.
	if got := ServiceTierForConfiguredRequest(supported, "priority", false); got != "" {
		t.Fatalf("priority without fast mode = %q, want empty", got)
	}
	if got := ServiceTierForConfiguredRequest(supported, "priority", true); got != "priority" {
		t.Fatalf("priority with fast mode = %q, want priority", got)
	}
	if got := ServiceTierForConfiguredRequest(noTiers, "priority", true); got != "" {
		t.Fatalf("unsupported priority = %q, want empty", got)
	}
	if got := ServiceTierForConfiguredRequest(supported, "default", true); got != "" {
		t.Fatalf("default = %q, want empty on the wire", got)
	}
	// Rust compares the configured value exactly, so only whitespace is trimmed.
	if !IsFlexServiceTier("  flex  ") || IsFlexServiceTier("FLEX") || IsFlexServiceTier("priority") {
		t.Fatal("IsFlexServiceTier did not match Rust's exact comparison")
	}
}

// Mirrors Rust #46230: Bedrock only supports the implicit default tier, even
// when a custom catalog advertises one, so the request omits `service_tier`.
func TestResponsesAgentRunnerOmitsServiceTierForBedrockLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name         string
		providerName string
		wantTier     any
	}{
		{name: "openai keeps flex", providerName: OpenAIProviderName, wantTier: "flex"},
		{name: "bedrock omits the tier", providerName: AmazonBedrockProviderName, wantTier: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var recordedBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&recordedBody); err != nil {
					t.Errorf("Decode request body error = %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"id":"resp-1",
					"model":"gpt-flex",
					"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
					"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
				}`))
			}))
			defer server.Close()

			authHeaders := BearerAuthHeaders("sk-test", "", false)
			runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
				Provider: &APIProvider{Name: tc.providerName, BaseURL: server.URL + "/v1"},
				Auth:     &authHeaders,
				ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
					Slug:         "gpt-flex",
					ServiceTiers: []string{"priority", "flex"},
				}}}),
			})
			if _, err := runner.Run(context.Background(), &AgentRequest{
				Model:       "gpt-flex",
				Prompt:      "hello",
				ServiceTier: "flex",
			}); err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if got := recordedBody["service_tier"]; got != tc.wantTier {
				t.Fatalf("service_tier = %#v, want %#v (body=%#v)", got, tc.wantTier, recordedBody)
			}
		})
	}
}
