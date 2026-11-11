package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/auth"
)

func captureIncrementalRequestBody(t *testing.T, providerName string, slug string, lite bool, incremental bool, input []any) map[string]any {
	t.Helper()
	recorded := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&recorded); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"` + slug + `","output_text":"ok","output":[]}`))
	}))
	t.Cleanup(server.Close)
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: providerName, BaseURL: server.URL + "/v1"},
		AuthSnapshot: &auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens:   map[string]any{"access_token": "token"},
		},
		ModelsManager: NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug:             slug,
			InputModalities:  []string{"text"},
			UseResponsesLite: lite,
		}}}),
	})
	tools := []any{
		map[string]any{"type": "function", "name": "echo"},
		map[string]any{"type": "web_search"},
	}
	if _, err := runner.Run(context.Background(), &AgentRequest{
		Model:            slug,
		Prompt:           "hello",
		Instructions:     "Use the available tools.",
		InputItems:       input,
		Tools:            tools,
		IncrementalTools: incremental,
	}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	return recorded
}

// TestIncrementalToolsLegacyKeepsWebSearchTopLevelLikeRust covers Rust #59832:
// a regular Amazon Bedrock Responses window that opts into incremental tools
// records the function catalog in `additional_tools`, while hosted web search
// stays in the top-level tools field. Rust #60040 (762f87d5bb) narrowed the
// legacy gate from OpenAI to the two bundled Bedrock providers.
func TestIncrementalToolsLegacyKeepsWebSearchTopLevelLikeRust(t *testing.T) {
	body := captureIncrementalRequestBody(t, AmazonBedrockProviderName, "gpt-regular", false, true, nil)

	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || !hasResponseToolType(tools, "web_search") {
		t.Fatalf("top-level tools = %#v, want only web_search", tools)
	}
	if hasResponseToolType(tools, "function") {
		t.Fatalf("function catalog leaked into top-level tools: %#v", tools)
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) == 0 {
		t.Fatalf("input = %#v", body["input"])
	}
	additional, ok := input[0].(map[string]any)
	if !ok || additional["type"] != "additional_tools" {
		t.Fatalf("first input item = %#v, want additional_tools", input[0])
	}
	declared, _ := additional["tools"].([]any)
	if !hasResponseToolType(declared, "function") || hasResponseToolType(declared, "web_search") {
		t.Fatalf("additional_tools = %#v, want functions without web_search", declared)
	}
	if instructions, ok := body["instructions"].(string); ok && instructions != "" {
		t.Fatalf("incremental request kept top-level instructions: %q", instructions)
	}
}

// TestIncrementalToolsLegacyOffKeepsFullCatalogLikeRust ensures the regular
// Responses layout stays unchanged unless the window opts in.
func TestIncrementalToolsLegacyOffKeepsFullCatalogLikeRust(t *testing.T) {
	body := captureIncrementalRequestBody(t, AmazonBedrockProviderName, "gpt-regular", false, false, nil)
	tools, _ := body["tools"].([]any)
	if !hasResponseToolType(tools, "function") || !hasResponseToolType(tools, "web_search") {
		t.Fatalf("top-level tools = %#v, want the full catalog", tools)
	}
	input, _ := body["input"].([]any)
	if len(input) > 0 {
		if first, ok := input[0].(map[string]any); ok && first["type"] == "additional_tools" {
			t.Fatalf("non-incremental request recorded declarations: %#v", first)
		}
	}
}

// TestResponsesLiteKeepsWebSearchTopLevelLikeRust covers the same split for
// Responses Lite windows.
func TestResponsesLiteKeepsWebSearchTopLevelLikeRust(t *testing.T) {
	body := captureIncrementalRequestBody(t, OpenAIProviderName, "gpt-lite", true, false, nil)
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || !hasResponseToolType(tools, "web_search") {
		t.Fatalf("lite top-level tools = %#v, want only web_search", tools)
	}
	input, _ := body["input"].([]any)
	if len(input) == 0 {
		t.Fatalf("input = %#v", body["input"])
	}
	additional, _ := input[0].(map[string]any)
	declared, _ := additional["tools"].([]any)
	if hasResponseToolType(declared, "web_search") {
		t.Fatalf("lite additional_tools kept web_search: %#v", declared)
	}
}

// TestIncrementalToolsLegacyGatesOnBedrockProvidersLikeRust covers Rust #60040
// (762f87d5bb) and #60617: both bundled Amazon Bedrock providers and Google
// Cloud Vertex AI publish incremental tool declarations on the regular
// Responses route, while a regular OpenAI Responses window keeps the full
// declaration prefix even when it opts in.
func TestIncrementalToolsLegacyGatesOnBedrockProvidersLikeRust(t *testing.T) {
	for _, providerName := range []string{AmazonBedrockProviderName, AmazonBedrockRuntimeProviderID, GCPProviderName} {
		body := captureIncrementalRequestBody(t, providerName, "gpt-regular", false, true, nil)
		input, _ := body["input"].([]any)
		if len(input) == 0 {
			t.Fatalf("%s: input = %#v", providerName, body["input"])
		}
		first, _ := input[0].(map[string]any)
		if first["type"] != "additional_tools" {
			t.Fatalf("%s: first input item = %#v, want additional_tools", providerName, input[0])
		}
	}

	body := captureIncrementalRequestBody(t, OpenAIProviderName, "gpt-regular", false, true, nil)
	tools, _ := body["tools"].([]any)
	if !hasResponseToolType(tools, "function") {
		t.Fatalf("OpenAI regular Responses dropped the full catalog: %#v", tools)
	}
	input, _ := body["input"].([]any)
	if len(input) > 0 {
		if first, ok := input[0].(map[string]any); ok && first["type"] == "additional_tools" {
			t.Fatalf("OpenAI still published incremental declarations: %#v", first)
		}
	}
}
